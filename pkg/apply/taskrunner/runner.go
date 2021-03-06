// Copyright 2020 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package taskrunner

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/cli-utils/pkg/apply/event"
	"sigs.k8s.io/cli-utils/pkg/apply/poller"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	pollevent "sigs.k8s.io/cli-utils/pkg/kstatus/polling/event"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/cli-utils/pkg/object"
)

// NewTaskStatusRunner returns a new TaskStatusRunner.
func NewTaskStatusRunner(identifiers []object.ObjMetadata, statusPoller poller.Poller) *taskStatusRunner {
	return &taskStatusRunner{
		identifiers:  identifiers,
		statusPoller: statusPoller,

		baseRunner: newBaseRunner(newResourceStatusCollector(identifiers)),
	}
}

// taskStatusRunner is a taskRunner that executes a set of
// tasks while at the same time uses the statusPoller to
// keep track of the status of the resources.
type taskStatusRunner struct {
	identifiers  []object.ObjMetadata
	statusPoller poller.Poller

	baseRunner *baseRunner
}

// PollingOptions defines properties that is passed along to
// the statusPoller.
type PollingOptions struct {
	PollInterval time.Duration
	UseCache     bool
}

// Run starts the execution of the taskqueue. It will start the
// statusPoller and then pass the statusChannel to the baseRunner
// that does most of the work.
func (tsr *taskStatusRunner) Run(ctx context.Context, taskQueue chan Task,
	eventChannel chan event.Event, pollingOptions PollingOptions) error {
	statusCtx, cancelFunc := context.WithCancel(context.Background())
	statusChannel := tsr.statusPoller.Poll(statusCtx, tsr.identifiers, polling.Options{
		PollUntilCancelled: true,
		PollInterval:       pollingOptions.PollInterval,
		UseCache:           pollingOptions.UseCache,
		// Not actually in use since we use a separate collector to keep
		// track of the status for each resource.
		//TODO(mortent): Remove the aggregator from the polling engine
		// and implement it as a wrapper instead.
		DesiredStatus: status.CurrentStatus,
	})

	err := tsr.baseRunner.run(ctx, taskQueue, statusChannel, eventChannel)
	// cancel the statusPoller by cancelling the context.
	cancelFunc()
	// drain the statusChannel to make sure the lack of a consumer
	// doesn't block the shutdown of the statusPoller.
	for range statusChannel {
	}
	return err
}

// NewTaskRunner returns a new taskRunner. It can process taskqueues
// that does not contain any wait tasks.
func NewTaskRunner() *taskRunner {
	collector := newResourceStatusCollector([]object.ObjMetadata{})
	return &taskRunner{
		baseRunner: newBaseRunner(collector),
	}
}

// taskRunner is a simplified taskRunner that does not support
// wait tasks and does not provide any status updates for the
// resources. This is useful in situations where we are not interested
// in status, for example during dry-run.
type taskRunner struct {
	baseRunner *baseRunner
}

// Run starts the execution of the task queue. It delegates the
// work to the baseRunner, but gives it as nil channel as the statusChannel.
func (tr *taskRunner) Run(ctx context.Context, taskQueue chan Task,
	eventChannel chan event.Event) error {
	var nilStatusChannel chan pollevent.Event
	return tr.baseRunner.run(ctx, taskQueue, nilStatusChannel, eventChannel)
}

// newBaseRunner returns a new baseRunner using the given collector.
func newBaseRunner(collector *resourceStatusCollector) *baseRunner {
	return &baseRunner{
		collector: collector,
	}
}

// baseRunner provides the basic task runner functionality. It needs
// a channel that provides resource status updates in order to support
// wait tasks, but it can also be used with a nil statusChannel for
// cases where polling and waiting for status is not needed.
// This is not meant to be used directly. It is used by the
// taskRunner and the taskStatusRunner.
type baseRunner struct {
	collector *resourceStatusCollector
}

// run is the main function that implements the processing of
// tasks in the taskqueue. It sets up a loop where a single goroutine
// will process events from three different channels.
func (b *baseRunner) run(ctx context.Context, taskQueue chan Task,
	statusChannel <-chan pollevent.Event, eventChannel chan event.Event) error {
	// taskChannel is used by tasks running in a separate goroutine
	// to signal back to the main loop that the task is either finished
	// or it has failed.
	taskChannel := make(chan TaskResult)

	// Find and start the first task in the queue.
	currentTask, done := b.nextTask(taskQueue, taskChannel)
	if done {
		return nil
	}

	// abort is used to signal that something has failed, and
	// the task processing should end as soon as is possible. Only
	// wait tasks can be interrupted, so for all other tasks we need
	// to wait for the currently running one to finish before we can
	// exit.
	abort := false
	var abortReason error

	// We do this so we can set the doneCh to a nil channel after
	// it has been closed. This is needed to avoid a busy loop.
	doneCh := ctx.Done()

	for {
		select {
		// This processes status events from a channel, most likely
		// driven by the StatusPoller. All normal resource status update
		// events are passed through to the eventChannel. This means
		// that listeners of the eventChannel will get updates on status
		// even while other tasks (like apply tasks) are running.
		case statusEvent, ok := <-statusChannel:
			// If the statusChannel has closed or we are preparing
			// to abort the task processing, we just ignore all
			// statusEvents.
			//TODO(mortent): Check if a losed statusChannel might
			// create a busy loop here.
			if !ok || abort {
				continue
			}

			// An error event on the statusChannel means the StatusPoller
			// has encountered a problem so it can't continue. This means
			// the statusChannel will be closed soon.
			if statusEvent.EventType == pollevent.ErrorEvent {
				abort = true
				abortReason = fmt.Errorf("polling for status failed: %v",
					statusEvent.Error)
				// If the current task is a wait task, we just set it
				// to complete so we can exit the loop as soon as possible.
				completeIfWaitTask(currentTask, taskChannel)
				continue
			}

			// Forward all normal events to the eventChannel
			eventChannel <- event.Event{
				Type:        event.StatusType,
				StatusEvent: statusEvent,
			}

			// The collector needs to keep track of the latest status
			// for all resources so we can check whether wait task conditions
			// has been met.
			b.collector.resourceStatus(statusEvent.Resource.Identifier,
				statusEvent.Resource.Status)
			// If the current task is a wait task, we check whether
			// the condition has been met. If so, we complete the task.
			if wt, ok := currentTask.(*WaitTask); ok {
				if b.collector.conditionMet(wt.Identifiers, wt.Condition) {
					completeIfWaitTask(currentTask, taskChannel)
				}
			}
		// A message on the taskChannel means that the current task
		// has either completed or failed. If it has failed, we return
		// the error. If the abort flag is true, which means something
		// else has gone wrong and we are waiting for the current task to
		// finish, we exit.
		// If everything is ok, we fetch and start the next task.
		case msg := <-taskChannel:
			currentTask.ClearTimeout()
			if msg.Err != nil {
				return msg.Err
			}
			if abort {
				return abortReason
			}
			currentTask, done = b.nextTask(taskQueue, taskChannel)
			// If there are no more tasks, we are done. So just
			// return.
			if done {
				return nil
			}
		// The doneCh will be closed if the passed in context is cancelled.
		// If so, we just set the abort flag and wait for the currently running
		// task to complete before we exit.
		case <-doneCh:
			doneCh = nil // Set doneCh to nil so we don't enter a busy loop.
			abort = true
			completeIfWaitTask(currentTask, taskChannel)
		}
	}
}

// completeIfWaitTask checks if the current task is a wait task. If so,
// we invoke the complete function to complete it.
func completeIfWaitTask(currentTask Task, taskChannel chan TaskResult) {
	if wt, ok := currentTask.(*WaitTask); ok {
		wt.complete(taskChannel)
	}
}

// nextTask fetches the latest task from the taskQueue and
// starts it. If the taskQueue is empty, it the second
// return value will be true.
func (b *baseRunner) nextTask(taskQueue chan Task,
	taskChannel chan TaskResult) (Task, bool) {
	var tsk Task
	select {
	// If there is any tasks left in the queue, this
	// case statement will be executed.
	case t := <-taskQueue:
		tsk = t
	default:
		// Only happens when the channel is empty.
		return nil, true
	}

	switch st := tsk.(type) {
	case *WaitTask:
		// The wait tasks need to be handled specifically here. Before
		// starting a new wait task, we check if the condition is already
		// met. Without this check, a task might end up waiting for
		// status events when the condition is in fact already met.
		if b.collector.conditionMet(st.Identifiers, st.Condition) {
			st.startAndComplete(taskChannel)
		} else {
			tsk.Start(taskChannel)
		}
	default:
		tsk.Start(taskChannel)
	}
	return tsk, false
}

// TaskResult is the type returned from tasks once they have completed
// or failed. If it has failed or timed out, the Err property will be
// set.
type TaskResult struct {
	Err error
}

// timeoutError is a special error used by tasks when they have
// timed out.
type timeoutError struct {
	message string
}

func (te timeoutError) Error() string {
	return te.message
}

// IsTimeoutError checks whether a given error is
// a timeoutError.
func IsTimeoutError(err error) bool {
	if _, ok := err.(timeoutError); ok {
		return true
	}
	return false
}
