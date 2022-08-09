package goworker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/cihub/seelog"
	"github.com/go-redis/redis/v9"
)

var (
	logger      seelog.LoggerInterface
	client      *redis.Client
	ctx         context.Context
	initMutex   sync.Mutex
	initialized bool
)

var workerSettings WorkerSettings

type WorkerSettings struct {
	QueuesString   string
	queues         queuesFlag
	IntervalFloat  float64
	interval       intervalFlag
	Concurrency    int
	Connections    int
	URI            string
	Namespace      string
	ExitOnComplete bool
	IsStrict       bool
	UseNumber      bool
	SkipTLSVerify  bool
	TLSCertPath    string
	ForcePrune     bool
}

func SetSettings(settings WorkerSettings) {
	// force the flags to be parsed first before setting the configs, so
	// they won't overwrite the settings
	if err := flags(); err != nil {
		panic(fmt.Errorf("can't SetSettings because of %w", err))
	}
	workerSettings = settings
}

// Init initializes the goworker process. This will be
// called by the Work function, but may be used by programs
// that wish to access goworker functions and configuration
// without actually processing jobs.
func Init() error {
	initMutex.Lock()
	defer initMutex.Unlock()
	if !initialized {
		var err error
		logger, err = seelog.LoggerFromWriterWithMinLevel(os.Stdout, seelog.InfoLvl)
		if err != nil {
			return err
		}

		if err := flags(); err != nil {
			return err
		}
		ctx = context.Background()

		opts, err := redis.ParseURL(workerSettings.URI)
		if err != nil {
			return err
		}

		if len(workerSettings.TLSCertPath) > 0 {
			certPool, err := getCertPool()
			if err != nil {
				return err
			}
			opts.TLSConfig = &tls.Config{
				RootCAs:            certPool,
				InsecureSkipVerify: workerSettings.SkipTLSVerify,
			}
		}

		client = redis.NewClient(opts)
		err = client.Ping(ctx).Err()
		if err != nil {
			return err
		}

		initialized = true
	}

	return nil
}

func getCertPool() (*x509.CertPool, error) {
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	certs, err := ioutil.ReadFile(workerSettings.TLSCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q for the RootCA pool: %v", workerSettings.TLSCertPath, err)
	}
	if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
		return nil, fmt.Errorf("failed to append %q to the RootCA pool: %v", workerSettings.TLSCertPath, err)
	}
	return rootCAs, nil
}

// Close cleans up resources initialized by goworker. This
// will be called by Work when cleaning up. However, if you
// are using the Init function to access goworker functions
// and configuration without processing jobs by calling
// Work, you should run this function when cleaning up.
func Close() error {
	initMutex.Lock()
	defer initMutex.Unlock()
	if initialized {
		err := client.Close()
		if err != nil {
			return err
		}
		initialized = false
	}

	return nil
}

// Work starts the goworker process. Check for errors in
// the return value. Work will take over the Go executable
// and will run until a QUIT, INT, or TERM signal is
// received, or until the queues are empty if the
// -exit-on-complete flag is set.
func Work() error {
	err := Init()
	if err != nil {
		return err
	}
	defer Close()

	quit := Signals()

	poller, err := newPoller(workerSettings.queues, workerSettings.IsStrict)
	if err != nil {
		return err // it will be error only if os.Hostname() fails
	}
	jobs, err := poller.poll(time.Duration(workerSettings.interval), quit)
	if err != nil {
		return err
	}

	var monitor sync.WaitGroup
	var wk *worker

	for id := 0; id < workerSettings.Concurrency; id++ {
		worker, err := newWorker(strconv.Itoa(id), workerSettings.queues)
		if err != nil {
			return err // it will be error only if os.Hostname() fails
		}
		if wk == nil {
			wk = worker
		}
		worker.work(jobs, &monitor)
	}

	// Once all the workers have started we prune the dead ones
	// this way we prevent from pruning workers that have just
	// started and not registered to the Heartbeat in case
	// of ForcePrune is enabled.
	wk.pruneDeadWorkers(client)

	monitor.Wait()

	return nil
}
