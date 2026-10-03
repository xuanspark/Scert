package localreset

import (
	"context"
	"crypto/subtle"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

const MaxRecoveryRequests = 32

// RecoverySession 仅供受保护的本机连接使用，与邮箱密码重置阶段互斥。
type RecoverySession interface {
	RecoveryAvailable() bool
	ListRecoveries(context.Context, string) (deployment.PendingRecoveryPage, error)
	InspectRecovery(context.Context, string) (deployment.RecoveryInspection, error)
}

func validRecoveryRequest(request Request, nonce string) bool {
	if request.Version != Version || subtle.ConstantTimeCompare([]byte(request.Nonce), []byte(nonce)) != 1 ||
		request.Method != "" || request.Code != "" || request.Password != "" {
		return false
	}
	switch request.Action {
	case "recovery_list":
		return request.TransactionID == "" && (request.Cursor == "" || validRecoveryID(request.Cursor)) && noDispositionRequest(request)
	case "recovery_inspect":
		return request.Cursor == "" && validRecoveryID(request.TransactionID) && noDispositionRequest(request)
	case "recovery_prepare":
		return request.Cursor == "" && validRecoveryID(request.TransactionID) && validRecoveryAction(request.RecoveryAction) && request.DispositionID == "" && request.ConfirmationToken == ""
	case "recovery_execute":
		return request.Cursor == "" && request.TransactionID == "" && request.RecoveryAction == "" && validRecoverySecret(request.DispositionID) && validRecoverySecret(request.ConfirmationToken)
	case "recovery_result":
		return request.Cursor == "" && request.TransactionID == "" && request.RecoveryAction == "" && validRecoverySecret(request.DispositionID) && request.ConfirmationToken == ""
	}
	return false
}

func validRecoveryID(value string) bool {
	if len(value) == 0 || len(value) > 160 || value == "." || value == ".." {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '.' && c != '_' {
			return false
		}
	}
	return true
}

func (c *Client) RecoveryAvailable() bool { return c.recoveryAvailable }

func (c *Client) ListRecoveries(cursor string) (deployment.PendingRecoveryPage, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_list", Cursor: cursor})
	if err != nil {
		return deployment.PendingRecoveryPage{}, err
	}
	return *response.RecoveryPage, nil
}

func (c *Client) InspectRecovery(id string) (deployment.RecoveryInspection, error) {
	response, err := c.recoveryExchange(Request{Action: "recovery_inspect", TransactionID: id})
	if err != nil {
		return deployment.RecoveryInspection{}, err
	}
	return *response.Inspection, nil
}

func (c *Client) recoveryExchange(request Request) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fail := func() (Response, error) {
		c.stage = -1
		_ = c.Close()
		return Response{}, ErrProtocol
	}
	if !c.recoveryAvailable || (c.stage != 0 && c.stage != 3) {
		return fail()
	}
	if request.Action != "recovery_list" && request.Action != "recovery_inspect" && !c.dispositionAvailable {
		return fail()
	}
	request.Version, request.Nonce = Version, c.nonce
	if !validRecoveryRequest(request, c.nonce) || writeFrame(c.conn, request) != nil {
		return fail()
	}
	var response Response
	if readFrame(c.reader, &response) != nil || !validChallenge(response) || response.Nonce == c.nonce ||
		len(response.Methods) != 0 || response.RecoveryAvailable || response.DispositionAvailable {
		return fail()
	}
	if request.Action == "recovery_list" {
		if response.RecoveryPage == nil || response.Inspection != nil || response.InspectionPart != nil || hasDispositionPayload(response) || len(response.RecoveryPage.Items) > deployment.RecoveryPageSize {
			return fail()
		}
		previous := request.Cursor
		for _, item := range response.RecoveryPage.Items {
			if !validRecoveryID(item.ID) || item.ID <= previous {
				return fail()
			}
			previous = item.ID
		}
		if cursor := response.RecoveryPage.NextCursor; cursor != "" &&
			(len(response.RecoveryPage.Items) != deployment.RecoveryPageSize || cursor != previous) {
			return fail()
		}
	} else if request.Action == "recovery_inspect" {
		if hasDispositionPayload(response) {
			return fail()
		}
		if response.InspectionPart != nil {
			inspection, err := readInspectionParts(c.reader, response)
			if err != nil {
				return fail()
			}
			response.Inspection, response.InspectionPart = inspection, nil
		}
		if !validInspectionPayload(response.Inspection) || response.RecoveryPage != nil || response.Inspection.TransactionID != request.TransactionID {
			return fail()
		}
	} else if !c.dispositionAvailable || !validDispositionResponse(request, response) {
		return fail()
	}
	c.nonce, c.stage = response.Nonce, 3
	return response, nil
}
