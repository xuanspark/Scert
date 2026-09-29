package repository

import (
	"context"
	"strings"
)

type TaskCancellationHooks struct {
	Register   func(string, context.CancelFunc)
	Unregister func(string)
}

type taskCancellationHooksContextKey struct{}
type taskOperatorContextKey struct{}

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

// WithTaskOperator 将当前已认证操作者绑定到任务工作流上下文。
func WithTaskOperator(ctx context.Context, operatorID string) context.Context {
	return context.WithValue(ctx, taskOperatorContextKey{}, strings.TrimSpace(operatorID))
}

// TaskOperatorFromContext 读取任务工作流的创建人；后台调度任务没有操作者时返回空字符串。
func TaskOperatorFromContext(ctx context.Context) string {
	operatorID, _ := ctx.Value(taskOperatorContextKey{}).(string)
	return strings.TrimSpace(operatorID)
}
