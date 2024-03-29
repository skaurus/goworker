package goworker

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type process struct {
	Hostname string
	Pid      int
	ID       string
	Queues   []string
}

func newProcess(id string, queues []string) (*process, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	return &process{
		Hostname: hostname,
		Pid:      os.Getpid(),
		ID:       id,
		Queues:   queues,
	}, nil
}

func (p *process) String() string {
	return fmt.Sprintf("%s:%d-%s:%s", p.Hostname, p.Pid, p.ID, strings.Join(p.Queues, ","))
}

func (p *process) open(c *redis.Client) error {
	err := c.SAdd(ctx, fmt.Sprintf("%sworkers", workerSettings.Namespace), p.String()).Err()
	if err != nil {
		return err
	}

	err = c.Set(ctx, fmt.Sprintf("%sstat:processed:%v", workerSettings.Namespace, p), "0", 0).Err()
	if err != nil {
		return err
	}

	err = c.Set(ctx, fmt.Sprintf("%sstat:failed:%v", workerSettings.Namespace, p), "0", 0).Err()
	if err != nil {
		return err
	}

	// We set the heartbeat as the first thing
	err = c.HSet(ctx, fmt.Sprintf("%s%s", workerSettings.Namespace, heartbeatKey), p.String(), time.Now().Format(time.RFC3339)).Err()
	if err != nil {
		return err
	}

	return nil
}

func (p *process) close(c *redis.Client) error {
	logger.Infof("%v shutdown", p)
	err := c.SRem(ctx, fmt.Sprintf("%sworkers", workerSettings.Namespace), p.String()).Err()
	if err != nil {
		return err
	}

	err = c.Del(ctx, fmt.Sprintf("%sstat:processed:%s", workerSettings.Namespace, p)).Err()
	if err != nil {
		return err
	}

	err = c.Del(ctx, fmt.Sprintf("%sstat:failed:%s", workerSettings.Namespace, p)).Err()
	if err != nil {
		return err
	}

	err = c.HDel(ctx, fmt.Sprintf("%s%s", workerSettings.Namespace, heartbeatKey), p.String()).Err()
	if err != nil {
		return err
	}

	return nil
}

func (p *process) start(c *redis.Client) error {
	err := c.Set(ctx, fmt.Sprintf("%sworker:%s:started", workerSettings.Namespace, p), time.Now().String(), 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func (p *process) finish(c *redis.Client) error {
	err := c.Del(ctx, fmt.Sprintf("%sworker:%s", workerSettings.Namespace, p)).Err()
	if err != nil {
		return err
	}

	err = c.Del(ctx, fmt.Sprintf("%sworker:%s:started", workerSettings.Namespace, p)).Err()
	if err != nil {
		return err
	}

	return nil
}

func (p *process) fail(c *redis.Client) error {
	err := c.Incr(ctx, fmt.Sprintf("%sstat:failed", workerSettings.Namespace)).Err()
	if err != nil {
		return err
	}

	err = c.Incr(ctx, fmt.Sprintf("%sstat:failed:%s", workerSettings.Namespace, p)).Err()
	if err != nil {
		return err
	}

	return nil
}

func (p *process) queues(strict bool) []string {
	// If the queues order is strict then just return them.
	if strict {
		return p.Queues
	}

	// If not then we want to shuffle the queues before returning them.
	queues := make([]string, len(p.Queues))
	for i, v := range rand.Perm(len(p.Queues)) {
		queues[i] = p.Queues[v]
	}
	return queues
}
