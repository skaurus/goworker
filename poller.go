package goworker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
)

var (
	errorUnexpectedJob          = errors.New("code expects exactly two items - queue name and task json")
	errorCustomArgsAreNotASlice = errors.New("args from custom decoder must be a slice")
)

type poller struct {
	process
	isStrict bool
}

func newPoller(queues []string, isStrict bool) (*poller, error) {
	process, err := newProcess("poller", queues)
	if err != nil {
		return nil, err
	}
	return &poller{
		process:  *process,
		isStrict: isStrict,
	}, nil
}

func (p *poller) getJob(c *redis.Client, interval time.Duration) (*Job, error) {
	for _, queue := range p.queues(p.isStrict) {
		logger.Debugf("Checking %s", queue)

		results, err := c.BLPop(ctx, interval, fmt.Sprintf("%squeue:%s", workerSettings.Namespace, queue)).Result()
		if err != nil {
			// no jobs for now, continue on another queue
			if err == redis.Nil {
				continue
			}
			return nil, err
		}
		if len(results) > 0 {
			if len(results) != 2 {
				return nil, errorUnexpectedJob
			}
			// at 0 index we have a queue name
			task := results[1]
			logger.Debugf("Found job on %s", queue)

			job := &Job{Queue: queue}

			if customDecoder == nil {
				decoder := json.NewDecoder(bytes.NewReader([]byte(task)))
				if workerSettings.UseNumber {
					decoder.UseNumber()
				}

				if err := decoder.Decode(&job.Payload); err != nil {
					return nil, err
				}
			} else {
				class, args, err := customDecoder(task)
				if err != nil {
					return nil, err
				}

				job.Payload.Class = class

				// customDecoder has to have a fixed signature, so args are of
				// type interface{}; here we check that it is actually a slice
				val := reflect.ValueOf(args).Elem()
				if val.Kind() != reflect.Slice {
					return nil, errorCustomArgsAreNotASlice
				}

				// and then make it an explicit slice of interfaces
				sliceLen := val.Len()
				job.Payload.Args = make([]interface{}, sliceLen)
				for i := 0; i < sliceLen; i++ {
					job.Payload.Args[i] = val.Index(i).Interface()
				}
			}

			return job, nil
		}
	}

	return nil, nil
}

func (p *poller) poll(interval time.Duration, quit <-chan bool) (<-chan *Job, error) {
	err := p.open(client)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	err = p.start(client)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	jobs := make(chan *Job)
	go func() {
		defer func() {
			close(jobs)

			err = p.finish(client)
			if err != nil {
				err = errors.WithStack(err)
				_ = logger.Criticalf("Error on %v finishing working on %v: %+v", p, p.Queues, err)
				return
			}

			err = p.close(client)
			if err != nil {
				err = errors.WithStack(err)
				_ = logger.Criticalf("Error on %v closing client on %v: %+v", p, p.Queues, err)
				return
			}
		}()

		for {
			select {
			case <-quit:
				return
			default:
				job, err := p.getJob(client, interval)
				if err != nil {
					err = errors.WithStack(err)
					_ = logger.Criticalf("Error on %v getting job from %v: %+v", p, p.Queues, err)
					return
				}
				if job != nil {
					err = client.Incr(ctx, fmt.Sprintf("%sstat:processed:%v", workerSettings.Namespace, p)).Err()
					if err != nil {
						err = errors.WithStack(err)
						_ = logger.Errorf("Error on %v incrementing stat on %v: %+v", p, p.Queues, err)
						return
					}

					select {
					case jobs <- job:
					case <-quit:
						buf, err := json.Marshal(job.Payload)
						if err != nil {
							err = errors.WithStack(err)
							_ = logger.Criticalf("Error requeueing %v: %v", job, err)
							return
						}

						err = client.LPush(ctx, fmt.Sprintf("%squeue:%s", workerSettings.Namespace, job.Queue), buf).Err()
						if err != nil {
							err = errors.WithStack(err)
							_ = logger.Criticalf("Error requeueing %v: %v", job, err)
							return
						}

						return
					}
				} else if workerSettings.ExitOnComplete {
					return
				}
			}
		}
	}()

	return jobs, nil
}
