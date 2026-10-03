package localreset

import (
	"context"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// RecoveryPreparation 只用于受保护的本机通道，不包含可重构服务端确认对象的绑定数据。
type RecoveryPreparation struct {
	DispositionID string                           `json:"disposition_id"`
	Token         string                           `json:"token"`
	Summary       deployment.RecoveryReviewSummary `json:"summary"`
}

type RecoveryDispositionSession interface {
	DispositionAvailable() bool
	PrepareRecovery(context.Context, string, deployment.RecoveryAction) (RecoveryPreparation, error)
	ExecuteRecovery(context.Context, string, string) (deployment.RecoveryDispositionSummary, error)
	GetRecoveryDisposition(context.Context, string) (deployment.RecoveryDispositionSummary, error)
}

type RecoveryReconciliationSession interface {
	PrepareRecoveryReconciliation(context.Context, string) (RecoveryPreparation, error)
}

func validRecoveryAction(action deployment.RecoveryAction) bool {
	return action == deployment.RecoveryActionRecord || action == deployment.RecoveryActionCommit || action == deployment.RecoveryActionRollback
}

func validRecoverySecret(value string) bool {
	if len(value) != 64 || value == strings.Repeat("0", 64) {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func noDispositionRequest(request Request) bool {
	return request.RecoveryAction == "" && request.DispositionID == "" && request.ConfirmationToken == ""
}

func hasDispositionPayload(response Response) bool {
	return response.DispositionAvailable || response.Preparation != nil || response.Disposition != nil
}

func validPreparation(value *RecoveryPreparation) bool {
	if value == nil || !validRecoverySecret(value.DispositionID) || !validRecoverySecret(value.Token) {
		return false
	}
	return value.Summary.Validate() == nil
}

func validDispositionResponse(request Request, response Response) bool {
	if response.RecoveryPage != nil || response.Inspection != nil || response.InspectionPart != nil {
		return false
	}
	if request.Action == "recovery_prepare" {
		return response.Disposition == nil && validPreparation(response.Preparation) &&
			response.Preparation.Summary.TransactionID == request.TransactionID && response.Preparation.Summary.Action == request.RecoveryAction
	}
	if request.Action == "recovery_reconcile_prepare" {
		return response.Disposition == nil && validPreparation(response.Preparation) &&
			response.Preparation.Summary.SourceDispositionID == request.DispositionID &&
			response.Preparation.DispositionID != request.DispositionID
	}
	return response.Preparation == nil && response.Disposition != nil && response.Disposition.Validate() == nil && response.Disposition.ID == request.DispositionID
}

func dispatchRecoveryDisposition(ctx context.Context, session RecoverySession, request Request, response *Response) error {
	actions, ok := session.(RecoveryDispositionSession)
	if !ok || !actions.DispositionAvailable() {
		return ErrUnavailable
	}
	var err error
	switch request.Action {
	case "recovery_reconcile_prepare":
		reconciler, ok := session.(RecoveryReconciliationSession)
		if !ok {
			return ErrUnavailable
		}
		var value RecoveryPreparation
		value, err = reconciler.PrepareRecoveryReconciliation(ctx, request.DispositionID)
		response.Preparation = &value
	case "recovery_prepare":
		var value RecoveryPreparation
		value, err = actions.PrepareRecovery(ctx, request.TransactionID, request.RecoveryAction)
		response.Preparation = &value
	case "recovery_execute":
		var value deployment.RecoveryDispositionSummary
		value, err = actions.ExecuteRecovery(ctx, request.DispositionID, request.ConfirmationToken)
		response.Disposition = &value
	case "recovery_result":
		var value deployment.RecoveryDispositionSummary
		value, err = actions.GetRecoveryDisposition(ctx, request.DispositionID)
		response.Disposition = &value
	default:
		return ErrProtocol
	}
	if err != nil || !validDispositionResponse(request, *response) {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) DispositionAvailable() bool { return c.dispositionAvailable }

func (c *Client) PrepareRecovery(id string, action deployment.RecoveryAction) (RecoveryPreparation, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_prepare", TransactionID: id, RecoveryAction: action})
	if err != nil {
		return RecoveryPreparation{}, err
	}
	return *response.Preparation, nil
}

func (c *Client) PrepareRecoveryReconciliation(sourceID string) (RecoveryPreparation, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_reconcile_prepare", DispositionID: sourceID})
	if err != nil {
		return RecoveryPreparation{}, err
	}
	return *response.Preparation, nil
}

func (c *Client) ExecuteRecovery(id, token string) (deployment.RecoveryDispositionSummary, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_execute", DispositionID: id, ConfirmationToken: token})
	if err != nil {
		return deployment.RecoveryDispositionSummary{}, err
	}
	return *response.Disposition, nil
}

func (c *Client) GetRecoveryDisposition(id string) (deployment.RecoveryDispositionSummary, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_result", DispositionID: id})
	if err != nil {
		return deployment.RecoveryDispositionSummary{}, err
	}
	return *response.Disposition, nil
}
