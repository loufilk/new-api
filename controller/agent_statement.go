package controller

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ExecuteAgentStatement(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	actions := map[string]string{"evidence-propose": "statement.evidence.propose", "evidence-confirm": "statement.evidence.confirm", "evidence-reject": "statement.evidence.reject", "import": "statement.import", "match-propose": "statement.match.propose", "match-confirm": "statement.match.confirm", "match-reject": "statement.match.reject", "exception-resolve": "statement.exception.resolve", "period-create": "statement.period.create", "period-close": "statement.period.close", "period-reopen": "statement.period.reopen"}
	action, ok := actions[c.Param("action")]
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentStatementInvalid)
		return
	}
	var command model.AgentStatementCommand
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, model.AgentStatementMaximumBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if common.IsRequestBodyTooLargeError(err) {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("The encoded statement is too large. Split the file without duplicating transactions."))
		return
	}
	if err != nil || common.DecodeJsonStrict(bytes.NewReader(body), &command) != nil {
		writeAgentAgreementError(c, model.ErrAgentStatementInvalid)
		return
	}
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	replay, found, err := model.ReplayAgentStatementOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	if found {
		common.ApiSuccess(c, replay)
		return
	}
	prepared, err := model.PrepareAgentStatementOperation(identity, action, command.RequestID, payload)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, prepared.VerificationContext)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	result, err := model.ExecuteAgentStatement(auth, action, command)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentStatements(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	page := redemptionPage(c)
	period, _ := strconv.ParseInt(c.Query("period_id"), 10, 64)
	importID, _ := strconv.ParseInt(c.Query("import_id"), 10, 64)
	line, _ := strconv.ParseInt(c.Query("line_id"), 10, 64)
	rows, total, err := model.ListAgentStatements(identity, c.Param("kind"), model.AgentStatementQuery{PeriodID: period, ImportID: importID, LineID: line, State: c.Query("state"), Offset: page.GetStartIdx(), Limit: page.GetPageSize()})
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func GetAgentStatementDetail(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAgentAgreementError(c, model.ErrAgentStatementInvalid)
		return
	}
	var result any
	switch c.Param("kind") {
	case "evidence":
		result, err = model.GetAgentStatementSupplementaryEvidence(identity, id)
	case "imports":
		result, err = model.GetAgentStatementImport(identity, id)
	case "periods":
		result, err = model.GetAgentStatementPeriod(identity, id)
	case "lines":
		result, err = model.GetAgentStatementLine(identity, id)
	case "matches":
		result, err = model.GetAgentStatementMatch(identity, id)
	case "exceptions":
		result, err = model.GetAgentStatementException(identity, id)
	default:
		err = model.ErrAgentStatementInvalid
	}
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentStatementCandidates(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAgentAgreementError(c, model.ErrAgentStatementInvalid)
		return
	}
	result, err := model.GetAgentStatementCandidates(identity, id)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentStatementEvidence(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentAgreementError(c, model.ErrAgentProfilePermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAgentAgreementError(c, model.ErrAgentStatementInvalid)
		return
	}
	result, err := model.GetAgentStatementEvidence(identity, id)
	if err != nil {
		writeAgentAgreementError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
