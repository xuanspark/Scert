package repository

import "context"

type TaskCancellationHooks struct {
	Register   func(string, context.CancelFunc)
	Unregister func(string)
}

type taskCancellationHooksContextKey struct{}

func WithTaskCancellationHooks(ctx context.Context, hooks TaskCancellationHooks) context.Context {
	return context.WithValue(ctx, taskCancellationHooksContextKey{}, hooks)
}

func TaskCancellationHooksFromContext(ctx context.Context) (TaskCancellationHooks, bool) {
	hooks, ok := ctx.Value(taskCancellationHooksContextKey{}).(TaskCancellationHooks)
	if !ok || hooks.Register == nil || hooks.Unregister == nil {
		return TaskCancellationHooks{}, false
	}
	return hooks, true
}
