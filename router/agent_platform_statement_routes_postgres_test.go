package router

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the actual mounted Gin tree, not a controller-only test router.
// Its static /statements/lines subtree must not hide the generic list/detail
// handlers, including the detail reload immediately after proposing a match.
func TestAgentPlatformRoutedStatementLineTreeAndAccessPostgres(t *testing.T) {
	f := newAgentPrepaidRouterFixture(t)
	var policy model.AgentPolicy
	require.NoError(t, model.DB.Order("id DESC").First(&policy).Error)
	policy.ID = 0
	policy.RequestID = "router-manual-statement-policy"
	policy.ManualReconciliationEnabled = true
	require.NoError(t, model.ValidateAgentPolicy(&policy))
	require.NoError(t, model.DB.Create(&policy).Error)
	require.NoError(t, model.DB.Model(&model.AgentPolicyState{}).Where("id = 1").Update("current_policy_id", policy.ID).Error)
	order := agentPrepaidCreditThroughRouter(t, f)
	var receipt model.AgentPaymentReceipt
	require.NoError(t, model.DB.First(&receipt, order.PaymentReceiptID).Error)

	periodInput := model.AgentStatementCommand{RequestID: "router-statement-period", Reason: "Synthetic routed bank statement period", AccountDossierReference: receipt.MerchantScope, Currency: receipt.Currency, StartsAt: receipt.PaidAt - 60, EndsAt: receipt.PaidAt + 3600}
	var periodResult model.AgentStatementResult
	agentFinanceSubmit(t, f.engine, f.finance, "statement.period.create", periodInput.RequestID, "/statements/period-create", periodInput, &periodResult)
	importInput := model.AgentStatementCommand{RequestID: "router-statement-import", Reason: "Synthetic routed manual statement import", PeriodID: periodResult.PeriodID, ExpectedVersion: 1, SourceReference: "document:routed-bank-statement", Lines: []model.AgentStatementLineInput{{ExternalReference: receipt.ProviderReference, OccurredAt: receipt.PaidAt, Direction: "incoming", Currency: receipt.Currency, AmountMinor: receipt.PaidMinor, BusinessReference: receipt.LocalOrderKey, EvidenceReference: "document:routed-bank-line"}}}
	var imported model.AgentStatementResult
	agentFinanceSubmit(t, f.engine, f.finance, "statement.import", importInput.RequestID, "/statements/import", importInput, &imported)
	var line model.AgentStatementLine
	require.NoError(t, model.DB.Where("import_id = ?", imported.ResourceID).First(&line).Error)
	propose := model.AgentStatementCommand{RequestID: "router-statement-propose", Reason: "Synthetic exact verified incoming receipt", LineID: line.ID, ExpectedVersion: 1, TargetType: "purchase", TargetID: int64(order.ID)}
	var match model.AgentStatementResult
	agentFinanceSubmit(t, f.engine, f.finance, "statement.match.propose", propose.RequestID, "/statements/match-propose", propose, &match)
	require.Equal(t, "proposed", match.State)

	linePath := fmt.Sprintf("/statements/lines/%d", line.ID)
	var detail model.AgentStatementLineView
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, linePath, nil, "", http.StatusOK, &detail)
	assert.Equal(t, line.ID, detail.ID)
	assert.Equal(t, "proposed", detail.Review.State)
	assert.Equal(t, match.ResourceID, detail.Review.MatchID)
	confirm := model.AgentStatementCommand{RequestID: "router-statement-confirm", Reason: "Synthetic independent bank match review", LineID: line.ID, MatchID: match.ResourceID, ExpectedVersion: detail.Review.Version}
	var confirmed model.AgentStatementResult
	agentFinanceSubmit(t, f.engine, f.reviewer, "statement.match.confirm", confirm.RequestID, "/statements/match-confirm", confirm, &confirmed)
	require.Equal(t, "confirmed", confirmed.State)

	var listed struct {
		Items []model.AgentStatementLineView `json:"items"`
		Total int64                          `json:"total"`
	}
	// Query input cannot override the server-selected lines resource kind.
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, "/statements/lines?kind=exceptions", nil, "", http.StatusOK, &listed)
	require.Len(t, listed.Items, 1)
	assert.EqualValues(t, 1, listed.Total)
	assert.Equal(t, line.ID, listed.Items[0].ID)
	assert.Equal(t, "confirmed", listed.Items[0].Review.State)
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, linePath, nil, "", http.StatusOK, &detail)
	assert.Equal(t, "confirmed", detail.Review.State)
	var candidates []model.AgentStatementCandidate
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, linePath+"/candidates", nil, "", http.StatusOK, &candidates)
	require.Len(t, candidates, 1)
	assert.Equal(t, receipt.ID, candidates[0].TargetID)
	assert.Empty(t, candidates[0].ExceptionCode)
	var evidence model.AgentStatementEvidenceDescriptor
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, linePath+"/evidence", nil, "", http.StatusOK, &evidence)
	assert.Equal(t, line.ID, evidence.LineID)
	assert.Equal(t, match.ResourceID, evidence.MatchID)
	assert.True(t, evidence.ReconciliationOnly)

	// Other generic statement kinds still resolve alongside the static subtree.
	for _, kind := range []string{"periods", "imports", "matches", "exceptions", "evidence", "audit"} {
		agentPrepaidRoute(t, f, f.finance, http.MethodGet, "/statements/"+kind, nil, "", http.StatusOK, nil)
	}
	for _, path := range []string{fmt.Sprintf("/statements/periods/%d", periodResult.PeriodID), fmt.Sprintf("/statements/imports/%d", imported.ResourceID), fmt.Sprintf("/statements/matches/%d", match.ResourceID)} {
		agentPrepaidRoute(t, f, f.finance, http.MethodGet, path, nil, "", http.StatusOK, nil)
	}

	rawPAT := model.AccessTokenPrefix + strings.Repeat("a", 43)
	pat := &model.UserAccessToken{Name: "synthetic routed finance PAT", TokenHash: model.AccessTokenFingerprint(rawPAT), TokenHint: model.AccessTokenHint(rawPAT), ExpiresAt: common.GetTimestamp() + 3600}
	require.NoError(t, pat.SetScopes([]string{"*"}))
	require.NoError(t, model.CreateUserAccessToken(f.finance.identity.UserID, pat, service.AccessTokenMaxPerUser))
	for _, path := range []string{"/statements/lines", linePath, linePath + "/candidates", linePath + "/evidence"} {
		t.Run(path, func(t *testing.T) {
			agentPrepaidRoute(t, f, f.owner, http.MethodGet, path, nil, "", http.StatusForbidden, nil)
			agentPrepaidRoute(t, f, agentFinanceBrowser{}, http.MethodGet, path, nil, "", http.StatusUnauthorized, nil)
			agentPrepaidRoute(t, f, agentFinanceBrowser{bearer: rawPAT}, http.MethodGet, path, nil, "", http.StatusForbidden, nil)
		})
	}
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 1000, 0, 0)
}
