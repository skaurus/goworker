# goworker

![Build](https://github.com/skaurus/goworker/workflows/Go/badge.svg)
[![GoDoc](https://godoc.org/github.com/benmanns/goworker?status.svg)](https://godoc.org/github.com/benmanns/goworker)
[![Build Status](https://app.travis-ci.com/skaurus/goworker.svg?branch=master)](https://app.travis-ci.com/skaurus/goworker)
[![Go Report Card](https://goreportcard.com/badge/github.com/skaurus/goworker)](https://goreportcard.com/report/github.com/skaurus/goworker)

goworker is a Resque-compatible, Go-based background worker. It allows you to push jobs into a queue using an expressive language like Ruby while harnessing the efficiency and concurrency of Go to minimize job latency and cost.

goworker workers can run alongside Ruby Resque clients so that you can keep all but your most resource-intensive jobs in Ruby.

## Introduction

This is a fork of a [bennmans' library goworker](https://github.com/benmanns/goworker). I integrated here work from:
- [Jberlinsky](https://github.com/benmanns/goworker/pull/62) on reporting error stacks to failed queue (replacing lib used to produce stacks)
- [kerak19](https://github.com/benmanns/goworker/pull/85) on replacing redis.go with go-redis (but using v9 instead of v7)
- updated example in README to use correct parameter IntervalFloat instead of Interval which gets overwritten later
- [FrankChung](https://github.com/benmanns/goworker/pull/46) to prevent overwriting WorkerSettings
- [cthulhu](https://github.com/benmanns/goworker/pull/56) to... I didn't quite figure out what was happening without this PR, to be honest
- [xescugc](https://github.com/benmanns/goworker/pull/87) to add heartbeats
- [clalimarmo](https://github.com/benmanns/goworker/pull/7) to enable cleanup of closure resources
- my own changes to get rid of some warnings, mostly about unhandled errors from `logger.Criticalf`
- [xescugs again #1](https://github.com/cycloidio/goworker/pull/4) - kudos to him for contacting me about these additional fixes
- [xescugs again #2](https://github.com/cycloidio/goworker/pull/8) to prune all workers not mentioned in a heartbeat list
- [here](https://github.com/skaurus/goworker/pull/17) I added an option to pass a ready Redis client to the lib. If it is not a go-redis v9 instance, you have no guarantees that it will work as expected.
- [and here](https://github.com/skaurus/goworker/pull/17) I added an option to pass a context.Context to the lib. By default it will create a new one via context.Background(). This context is used in all Redis calls.
- [and here](https://github.com/skaurus/goworker/pull/20) I replaced LPOP + sleep with a BLPOP
- [and here](https://github.com/skaurus/goworker/pull/21) I started logging worker errors to log
- [and here](https://github.com/skaurus/goworker/pull/22) I added a RegisterDecoder method, so that your custom types, which were encoded to JSON as payload args, could be decoded to same custom types instead of generic `map[string]interface{}`

Also [this PR](https://github.com/cycloidio/goworker/pull/9) might be of interest for some, but I did not merge it.

## Installation

To install goworker, use

```sh
go get github.com/skaurus/goworker
```

to install the package, and then from your worker

```go
import "github.com/skaurus/goworker"
```

## Getting Started

To create a worker, write a function matching the signature

```go
func(string, ...interface{}) error
```

and register it using

```go
goworker.Register("MyClass", myFunc)
```

Here is a simple worker that prints its arguments:

```go
package main

import (
	"fmt"

	"github.com/skaurus/goworker"
)

func myFunc(queue string, args ...interface{}) error {
	fmt.Printf("From %s, %v\n", queue, args)
	return nil
}

func init() {
	goworker.Register("MyClass", myFunc)
}

func main() {
	if err := goworker.Work(); err != nil {
		fmt.Println("Error:", err)
	}
}
```

To create workers that share a database pool or other resources, use a closure to share variables.

```go
package main

import (
	"fmt"

	"github.com/skaurus/goworker"
)

func newMyFunc(uri string) (func(queue string, args ...interface{}) error) {
	foo := NewFoo(uri)

	quit := goworker.Signals()
	go func() {
		<-quit
		// release any resources held by foo
		foo.CleanUp()
	}()
	
	return func(queue string, args ...interface{}) error {
		foo.Bar(args)
		return nil
	}
}

func init() {
	goworker.Register("MyClass", newMyFunc("http://www.example.com/"))
}

func main() {
	if err := goworker.Work(); err != nil {
		fmt.Println("Error:", err)
	}
}
```

Here is a simple worker with settings:

```go
package main

import (
	"fmt"

	"github.com/skaurus/goworker"
)

func myFunc(queue string, args ...interface{}) error {
	fmt.Printf("From %s, %v\n", queue, args)
	return nil
}

func init() {
	settings := goworker.WorkerSettings{
		URI:            "redis://localhost:6379/",
		// or you can pass ready client instead of URI
		// (it takes precedence over URI and must be go-redis v9 client)
		Redis:          client,
		Connections:    100,
		// this setting takes precedence over -queues flag
		QueuesString:   "myqueue,delimited,queues",
		ExitOnComplete: false,
		Concurrency:    2,
		Namespace:      "resque:",
		// timeout for BLPOP in seconds
		IntervalFloat:  5.0,
	}
	goworker.SetSettings(settings)
	goworker.Register("MyClass", myFunc)
}

func main() {
	if err := goworker.Work(); err != nil {
		fmt.Println("Error:", err)
	}
}
```

goworker worker functions receive the queue they are serving and a slice of interfaces. To use them as parameters to other functions, use Go type assertions to convert them into usable types.

```go
// Expecting (int, string, float64)
func myFunc(queue, args ...interface{}) error {
	idNum, ok := args[0].(json.Number)
	if !ok {
		return errorInvalidParam
	}
	id, err := idNum.Int64()
	if err != nil {
		return errorInvalidParam
	}
	name, ok := args[1].(string)
	if !ok {
		return errorInvalidParam
	}
	weightNum, ok := args[2].(json.Number)
	if !ok {
		return errorInvalidParam
	}
	weight, err := weightNum.Float64()
	if err != nil {
		return errorInvalidParam
	}
	doSomething(id, name, weight)
	return nil
}
```

Sometimes Go assertions are not enough - for example, when one of arguments is a custom `struct` type, it will end up in goworker as `map[string]interface{}`, and casting it back to `struct` does not work (`interface {} is map[string]interface {}, not customType`).  
In this case you can register custom decoder:

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/skaurus/goworker"
)

func customDecoder(job string) (class string, args interface{}, err error) {
	payload := struct {
		Class string
		Args  []customType
	}{}
	err = json.Unmarshal([]byte(job), &payload)
	return payload.Class, payload.Args, err
}

func myFunc(queue string, args ...interface{}) error {
	fmt.Printf("From %s, %v\n", queue, args)
	customTypes := make([]customType, 0, len(args))
    for _, task := range args {
        event, ok := task.(customType)
        if !ok {
            continue
        }
		customTypes = append(customTypes, event)
    }
	fmt.Printf("From %s => %v\n", queue, customTypes)
	return nil
}

func init() {
	goworker.RegisterDecoder(customDecoder)
	goworker.Register("MyClass", myFunc)
}

func main() {
	if err := goworker.Work(); err != nil {
		fmt.Println("Error:", err)
	}
}
```

There is some reflection involved when custom decoder is used, but I found no way around it. Well, one alternative solution is just marshal args back to JSON and unmarshal them to custom type, but I consider this ugly. But if you prefer, it could be done right in your registered function, with no need for custom decoder.

For testing, it is helpful to use the `redis-cli` program to insert jobs onto the Redis queue:

```sh
redis-cli -r 100 RPUSH resque:queue:myqueue '{"class":"MyClass","args":["hi","there"]}'
```

will insert 100 jobs for the `MyClass` worker onto the `myqueue` queue. It is equivalent to:

```ruby
class MyClass
  @queue = :myqueue
end

100.times do
  Resque.enqueue MyClass, ['hi', 'there']
end
```

or

```golang
goworker.Enqueue(&goworker.Job{
    Queue: "myqueue",
    Payload: goworker.Payload{
        Class: "MyClass",
        Args: []interface{}{"hi", "there"},
    },
})
```

## Flags

There are several flags which control the operation of the goworker client.

* `-queues="comma,delimited,queues"` — This is the only required flag (not if you set QueuesString setting though). The recommended practice is to separate your Resque workers from your goworkers with different queues. Otherwise, Resque worker classes that have no goworker analog will cause the goworker process to fail the jobs. Because of this, there is no default queue, nor is there a way to select all queues (à la Resque's `*` queue). If you have multiple queues you can assign them weights. A queue with a weight of 2 will be checked twice as often as a queue with a weight of 1: `-queues='high=2,low=1'`.
* `-interval=5.0` — Specifies the wait period between polling in seconds if no job was in the queue the last time one was requested.
* `-concurrency=25` — Specifies the number of concurrently executing workers. This number can be as low as 1 or rather comfortably as high as 100,000, and should be tuned to your workflow and the availability of outside resources.
* `-connections=2` — Specifies the maximum number of Redis connections that goworker will consume between the poller and all workers. There is not much performance gain over two and a slight penalty when using only one. This is configurable in case you need to keep connection counts low for cloud Redis providers who limit plans on `maxclients`.
* `-uri=redis://localhost:6379/` — Specifies the URI of the Redis database from which goworker polls for jobs. Accepts URIs of the format `redis://user:pass@host:port/db` or `unix:///path/to/redis.sock`. The flag may also be set by the environment variable `$($REDIS_PROVIDER)` or `$REDIS_URL`. E.g. set `$REDIS_PROVIDER` to `REDISTOGO_URL` on Heroku to let the Redis To Go add-on configure the Redis database.
* `-namespace=resque:` — Specifies the namespace from which goworker retrieves jobs and stores stats on workers.
* `-exit-on-complete=false` — Exits goworker when there are no jobs left in the queue. This is helpful in conjunction with the `time` command to benchmark different configurations.

You can also configure your own flags for use within your workers. Be sure to set them before calling `goworker.Main()`. It is okay to call `flags.Parse()` before calling `goworker.Main()` if you need to do additional processing on your flags.

## Signal Handling in goworker

To stop goworker, send a `QUIT`, `TERM`, or `INT` signal to the process. This will immediately stop job polling. There can be up to `$CONCURRENCY` jobs currently running, which will continue to run until they are finished.

## Failure Modes

Like Resque, goworker makes no guarantees about the safety of jobs in the event of process shutdown. Workers must be both idempotent and tolerant to loss of the job in the event of failure.

If the process is killed with a `KILL` or by a system failure, there may be one job that is currently in the poller's buffer that will be lost without any representation in either the queue or the worker variable.

If you are running goworker on a system like Heroku, which sends a `TERM` to signal a process that it needs to stop, ten seconds later sends a `KILL` to force the process to stop, your jobs must finish within 10 seconds or they may be lost. Jobs will be recoverable from the Redis database under

```
resque:worker:<hostname>:<process-id>-<worker-id>:<queues>
```

as a JSON object with keys `queue`, `run_at`, and `payload`, but the process is manual. Additionally, there is no guarantee that the job in Redis under the worker key has not finished, if the process is killed before goworker can flush the update to Redis.

## Contributing

1. [Fork it](https://github.com/skaurus/goworker/fork)
2. Create your feature branch (`git checkout -b my-new-feature`)
3. Commit your changes (`git commit -am 'Add some feature'`)
4. Push to the branch (`git push origin my-new-feature`)
5. Create new Pull Request
