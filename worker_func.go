package goworker

type workerFunc func(queue string, tasks ...interface{}) error

type decoderFunc func(job string) (class string, args interface{}, err error)
