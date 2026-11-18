package service

import "github.com/QuantumNous/new-api/model"

const (
	VerificationScopeReferralWithdrawalCreate = "referral.withdrawal.create"
	VerificationScopeReferralWithdrawalReview = "referral.withdrawal.review"
)

type ReferralWithdrawalCreateContext struct {
	RequestKey string                        `json:"request_key"`
	Input      model.ReferralWithdrawalInput `json:"input"`
}

type ReferralWithdrawalReviewContext struct {
	WithdrawalID int64                          `json:"withdrawal_id"`
	RequestKey   string                         `json:"request_key"`
	Input        model.ReferralWithdrawalAction `json:"input"`
}
