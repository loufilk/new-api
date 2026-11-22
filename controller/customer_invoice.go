package controller

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const invoiceJSONLimit = 32 << 10

var (
	errInvoiceMediaType = errors.New("unsupported invoice request media type")
	errInvoiceBodyLimit = errors.New("invoice request is too large")
)

// Return fixed public messages only. Driver, multipart, and decoder errors can
// contain private application fields, uploaded names, or database internals.
func customerInvoiceError(c *gin.Context, err error) {
	status, code, key := http.StatusInternalServerError, "INVOICE_UNAVAILABLE", "Database error, please contact the administrator"
	var sizeError *http.MaxBytesError
	switch {
	case errors.Is(err, errInvoiceBodyLimit), errors.Is(err, model.ErrCustomerInvoicePDFTooLarge), errors.As(err, &sizeError):
		status, code, key = http.StatusRequestEntityTooLarge, "INVOICE_TOO_LARGE", "The file is too large"
	case errors.Is(err, errInvoiceMediaType):
		status, code, key = http.StatusUnsupportedMediaType, "INVOICE_MEDIA_TYPE", "Invalid parameters"
	case errors.Is(err, model.ErrCustomerInvoiceInvalid), errors.Is(err, model.ErrCustomerInvoicePDFInvalid):
		status, code, key = http.StatusBadRequest, "INVOICE_INVALID", "Invalid parameters"
	case errors.Is(err, model.ErrCustomerInvoiceNotFound):
		status, code, key = http.StatusNotFound, "INVOICE_NOT_FOUND", "Financial record not found"
	case errors.Is(err, model.ErrCustomerInvoiceConflict):
		status, code, key = http.StatusConflict, "INVOICE_CONFLICT", "Conflict"
	case errors.Is(err, model.ErrCustomerInvoiceDisabled):
		status, code, key = http.StatusForbidden, "INVOICE_DISABLED", "Disabled by current policy"
	case errors.Is(err, model.ErrCustomerInvoiceSettingsConflict):
		status, code, key = http.StatusConflict, "INVOICE_SETTINGS_CONFLICT", "Invoice settings changed. Refresh and try again."
	case errors.Is(err, model.ErrCustomerInvoiceCategoryUnavailable):
		status, code, key = http.StatusConflict, "INVOICE_CATEGORY_UNAVAILABLE", "This invoice category is currently unavailable"
	case errors.Is(err, model.ErrCustomerInvoiceTypeDisabled):
		status, code, key = http.StatusForbidden, "INVOICE_TYPE_DISABLED", "This invoice type is currently unavailable"
	case errors.Is(err, model.ErrCustomerInvoiceFeePaymentUnavailable):
		status, code, key = http.StatusConflict, "INVOICE_FEE_PAYMENT_UNAVAILABLE", "Invoice fee payment is not available yet"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(key), gin.H{"code": code})
}

func respondCustomerInvoice(c *gin.Context, data any, err error) {
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func customerInvoiceID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return 0, false
	}
	return id, true
}

func customerInvoicePageQuery(c *gin.Context, status bool, admin bool) (model.CustomerInvoiceQuery, bool) {
	page, pageErr := strconv.Atoi(c.DefaultQuery("p", "1"))
	size, sizeErr := strconv.Atoi(c.DefaultQuery("size", "20"))
	query := model.CustomerInvoiceQuery{Page: page, Size: size}
	if pageErr != nil || sizeErr != nil || page < 1 || page > 1000000 || size < 1 || size > 100 {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return query, false
	}
	if status {
		query.Status = c.Query("status")
		switch query.Status {
		case "", "awaiting_payment", "pending", "issued", "rejected", "cancelled":
		case "refund_review", "fee_refund_review":
			if admin {
				break
			}
			fallthrough
		default:
			customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
			return query, false
		}
	}
	return query, true
}

func readCustomerInvoiceJSON(c *gin.Context, dst any) bool {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		customerInvoiceError(c, errInvoiceMediaType)
		return false
	}
	if c.Request.ContentLength > invoiceJSONLimit {
		customerInvoiceError(c, errInvoiceBodyLimit)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, invoiceJSONLimit)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			customerInvoiceError(c, errInvoiceBodyLimit)
		} else {
			customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		}
		return false
	}
	if common.DecodeJsonStrict(bytes.NewReader(data), dst) != nil {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return false
	}
	return true
}

func GetCustomerInvoiceConfig(c *gin.Context) {
	methods, walletRate := customerInvoiceFeeMethods()
	if !model.GetCustomerInvoicePolicy().Enabled {
		data := gin.H{"enabled": false}
		if len(methods) != 0 {
			data["fee_payment_methods"], data["wallet_quota_per_cent"] = methods, walletRate
		}
		common.ApiSuccess(c, data)
		return
	}
	settings, err := model.GetCustomerInvoiceSettings(c.Request.Context())
	if err != nil {
		respondCustomerInvoice(c, nil, err)
		return
	}
	settings.Categories = settings.Categories.EnabledOnly()
	respondCustomerInvoice(c, struct {
		Enabled bool `json:"enabled"`
		model.CustomerInvoiceSettings
		FeePaymentMethods  []customerInvoiceFeeMethod `json:"fee_payment_methods"`
		WalletQuotaPerCent int64                      `json:"wallet_quota_per_cent"`
	}{settings.CenterEnabled, settings, methods, walletRate}, nil)
}

type customerInvoiceFeeMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Rail string `json:"rail"`
}

func customerInvoiceFeeMethods() ([]customerInvoiceFeeMethod, int64) {
	if model.GetCustomerInvoicePolicy().MerchantScope == "" {
		return []customerInvoiceFeeMethod{}, 0
	}
	epay := CustomerInvoiceEpayMethods()
	wallet := invoiceWalletPaymentAvailable()
	methods := make([]customerInvoiceFeeMethod, 0, len(epay)+1)
	for _, method := range epay {
		methods = append(methods, customerInvoiceFeeMethod{ID: method["type"], Name: method["name"], Rail: "epay"})
	}
	if wallet > 0 {
		methods = append(methods, customerInvoiceFeeMethod{ID: "paid_wallet", Name: "Paid balance", Rail: "wallet"})
	}
	return methods, wallet
}

func invoiceWalletPaymentAvailable() int64 {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if !isPaymentComplianceConfirmed() {
		return 0
	}
	return model.CustomerInvoiceWalletQuotaPerCent()
}

func respondAdminInvoiceSettings(c *gin.Context, settings model.CustomerInvoiceSettings, err error) {
	methods, _ := customerInvoiceFeeMethods()
	respondCustomerInvoice(c, struct {
		model.CustomerInvoiceSettings
		FeePaymentAvailable bool `json:"fee_payment_available"`
	}{settings, len(methods) > 0}, err)
}

func GetAdminCustomerInvoiceSettings(c *gin.Context) {
	settings, err := model.GetCustomerInvoiceSettings(c.Request.Context())
	respondAdminInvoiceSettings(c, settings, err)
}

type invoiceTypeSettingsInput struct {
	Enabled    *bool `json:"enabled"`
	FeeEnabled *bool `json:"fee_enabled"`
	FeeRateBPS *int  `json:"fee_rate_bps"`
	TaxRateBPS *int  `json:"tax_rate_bps"`
}

func (input invoiceTypeSettingsInput) toSettings() (model.CustomerInvoiceTypeSettings, bool) {
	if input.Enabled == nil || input.FeeEnabled == nil || input.FeeRateBPS == nil || input.TaxRateBPS == nil {
		return model.CustomerInvoiceTypeSettings{}, false
	}
	return model.CustomerInvoiceTypeSettings{
		Enabled: *input.Enabled, FeeEnabled: *input.FeeEnabled, FeeRateBPS: *input.FeeRateBPS, TaxRateBPS: *input.TaxRateBPS,
	}, true
}

func UpdateAdminCustomerInvoiceSettings(c *gin.Context) {
	var input struct {
		CenterEnabled *bool                           `json:"center_enabled"`
		Categories    model.CustomerInvoiceCategories `json:"categories"`
		Version       *int64                          `json:"version"`
		Ordinary      invoiceTypeSettingsInput        `json:"ordinary"`
		Special       invoiceTypeSettingsInput        `json:"special"`
	}
	if !readCustomerInvoiceJSON(c, &input) {
		return
	}
	ordinary, ordinaryOK := input.Ordinary.toSettings()
	special, specialOK := input.Special.toSettings()
	if input.Version == nil || !ordinaryOK || !specialOK {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return
	}
	current, err := model.GetCustomerInvoiceSettings(c.Request.Context())
	if err != nil {
		respondAdminInvoiceSettings(c, model.CustomerInvoiceSettings{}, err)
		return
	}
	centerEnabled := current.CenterEnabled
	if input.CenterEnabled != nil {
		centerEnabled = *input.CenterEnabled
	}
	proposed := model.CustomerInvoiceSettings{CenterEnabled: centerEnabled, Version: *input.Version, Ordinary: ordinary, Special: special, Categories: input.Categories}
	settings, err := model.UpdateCustomerInvoiceSettings(c.Request.Context(), c.GetInt("id"), proposed)
	respondAdminInvoiceSettings(c, settings, err)
}

func GetCustomerInvoiceSummary(c *gin.Context) {
	totals, err := model.SummarizeCustomerInvoices(c.Request.Context(), c.GetInt("id"), model.GetCustomerInvoicePolicy())
	respondCustomerInvoice(c, totals, err)
}

func ListCustomerInvoiceOrders(c *gin.Context) {
	query, ok := customerInvoicePageQuery(c, false, false)
	if !ok {
		return
	}
	items, total, err := model.ListCustomerInvoiceOrders(c.Request.Context(), c.GetInt("id"), model.GetCustomerInvoicePolicy(), query)
	if items == nil {
		items = []model.CustomerInvoiceOrder{}
	}
	respondCustomerInvoice(c, gin.H{"items": items, "total": total}, err)
}

func ListCustomerInvoiceApplications(c *gin.Context)      { listCustomerInvoices(c, false) }
func ListAdminCustomerInvoiceApplications(c *gin.Context) { listCustomerInvoices(c, true) }

func listCustomerInvoices(c *gin.Context, admin bool) {
	query, ok := customerInvoicePageQuery(c, true, admin)
	if !ok {
		return
	}
	items, total, err := model.ListCustomerInvoices(c.Request.Context(), c.GetInt("id"), admin, query)
	if items == nil {
		items = []model.CustomerInvoiceSummary{}
	}
	respondCustomerInvoice(c, gin.H{"items": items, "total": total}, err)
}

func GetCustomerInvoice(c *gin.Context)      { getCustomerInvoice(c, false) }
func GetAdminCustomerInvoice(c *gin.Context) { getCustomerInvoice(c, true) }

func getCustomerInvoice(c *gin.Context, admin bool) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	detail, err := model.GetCustomerInvoice(c.Request.Context(), c.GetInt("id"), admin, id)
	respondCustomerInvoice(c, detail, err)
}

func SubmitCustomerInvoice(c *gin.Context) {
	policy := model.GetCustomerInvoicePolicy()
	if !policy.Enabled {
		customerInvoiceError(c, model.ErrCustomerInvoiceDisabled)
		return
	}
	var input model.CustomerInvoiceInput
	if !readCustomerInvoiceJSON(c, &input) {
		return
	}
	policy.EpayFeeAvailable = len(CustomerInvoiceEpayMethods()) > 0
	policy.WalletFeeAvailable = invoiceWalletPaymentAvailable() > 0
	detail, err := model.SubmitCustomerInvoice(c.Request.Context(), c.GetInt("id"), policy, input)
	respondCustomerInvoice(c, detail, err)
}

func emptyCustomerInvoiceBody(c *gin.Context) bool {
	if c.Request.ContentLength > 0 {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return false
	}
	// Do not let an unknown-length body bypass the empty-body contract.
	if c.Request.Body != nil {
		b, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
		if err != nil || len(b) != 0 {
			customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
			return false
		}
	}
	return true
}

func CancelCustomerInvoice(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok || !emptyCustomerInvoiceBody(c) {
		return
	}
	detail, err := model.CancelCustomerInvoice(c.Request.Context(), c.GetInt("id"), id)
	respondCustomerInvoice(c, detail, err)
}

func ClaimCustomerInvoice(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok || !emptyCustomerInvoiceBody(c) {
		return
	}
	// Claim can finish an existing application when new intake is disabled.
	// The model rechecks merchant and payment/refund evidence under the lock.
	detail, err := model.ClaimCustomerInvoice(c.Request.Context(), c.GetInt("id"), model.GetCustomerInvoicePolicy(), id)
	respondCustomerInvoice(c, detail, err)
}

// Require explicitly positive, unambiguous confirmation of no external issue.
// Reject strings, null and repeated confirmations (even repeated true values).
type invoiceReleaseConfirmation bool

func (flag *invoiceReleaseConfirmation) UnmarshalJSON(data []byte) error {
	if *flag || string(data) != "true" {
		return model.ErrCustomerInvoiceInvalid
	}
	*flag = true
	return nil
}

func ReleaseCustomerInvoiceClaim(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	var input struct {
		Confirm invoiceReleaseConfirmation `json:"confirm_not_issued"`
	}
	if !readCustomerInvoiceJSON(c, &input) {
		return
	}
	if !input.Confirm {
		customerInvoiceError(c, model.ErrCustomerInvoiceInvalid)
		return
	}
	detail, err := model.ReleaseCustomerInvoiceClaim(c.Request.Context(), c.GetInt("id"), id)
	respondCustomerInvoice(c, detail, err)
}

func RejectCustomerInvoice(c *gin.Context) {
	customerInvoiceFinanceReason(c, false)
}

func FlagCustomerInvoiceReconciliation(c *gin.Context) {
	customerInvoiceFinanceReason(c, true)
}

func customerInvoiceFinanceReason(c *gin.Context, reconciliation bool) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !readCustomerInvoiceJSON(c, &input) {
		return
	}
	var detail *model.CustomerInvoiceDetail
	var err error
	if reconciliation {
		detail, err = model.FlagCustomerInvoiceReconciliation(c.Request.Context(), c.GetInt("id"), id, input.Reason)
	} else {
		detail, err = model.RejectCustomerInvoice(c.Request.Context(), c.GetInt("id"), id, input.Reason)
	}
	respondCustomerInvoice(c, detail, err)
}

// Files are never persisted by the multipart parser: parse at most three known
// parts from a bounded stream. Ignore the client filename and MIME label; only
// NewCustomerInvoicePDF determines eligibility of the actual file contents.
func readCustomerInvoiceIssue(c *gin.Context) (string, model.CustomerInvoicePDF, error) {
	var empty model.CustomerInvoicePDF
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		return "", empty, errInvoiceMediaType
	}
	const maxBody = model.CustomerInvoicePDFMaxBytes + (64 << 10)
	if c.Request.ContentLength > maxBody {
		return "", empty, errInvoiceBodyLimit
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return "", empty, model.ErrCustomerInvoiceInvalid
	}
	seen := map[string]bool{}
	var number string
	var pdf model.CustomerInvoicePDF
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				return "", empty, errInvoiceBodyLimit
			}
			return "", empty, model.ErrCustomerInvoiceInvalid
		}
		name := part.FormName()
		if seen[name] || len(part.Header.Values("Content-Disposition")) != 1 || part.Header.Get("Content-Transfer-Encoding") != "" {
			return "", empty, model.ErrCustomerInvoiceInvalid
		}
		seen[name] = true
		switch name {
		case "file":
			if part.FileName() == "" {
				return "", empty, model.ErrCustomerInvoiceInvalid
			}
			pdf, err = model.NewCustomerInvoicePDF(part)
			if err != nil {
				var limit *http.MaxBytesError
				if errors.As(err, &limit) || errors.Is(err, model.ErrCustomerInvoicePDFTooLarge) {
					return "", empty, errInvoiceBodyLimit
				}
				return "", empty, model.ErrCustomerInvoicePDFInvalid
			}
		case "invoice_number", "confirm_payment_verified":
			if part.FileName() != "" {
				return "", empty, model.ErrCustomerInvoiceInvalid
			}
			value, err := io.ReadAll(io.LimitReader(part, 257))
			if err != nil || len(value) > 256 {
				return "", empty, model.ErrCustomerInvoiceInvalid
			}
			if name == "invoice_number" {
				number = strings.TrimSpace(string(value))
			} else if string(value) != "true" {
				return "", empty, model.ErrCustomerInvoiceInvalid
			}
		default:
			return "", empty, model.ErrCustomerInvoiceInvalid
		}
	}
	// A multipart epilogue must not evade the total-byte cap on chunked bodies.
	if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return "", empty, errInvoiceBodyLimit
		}
		return "", empty, model.ErrCustomerInvoiceInvalid
	}
	if number == "" || !seen["file"] || !seen["confirm_payment_verified"] {
		return "", empty, model.ErrCustomerInvoiceInvalid
	}
	return number, pdf, nil
}

func IssueCustomerInvoice(c *gin.Context) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	policy := model.GetCustomerInvoicePolicy()
	if policy.MerchantScope == "" {
		customerInvoiceError(c, model.ErrCustomerInvoiceDisabled)
		return
	}
	actor := c.GetInt("id")
	ctx := c.Request.Context()
	current, err := model.GetCustomerInvoice(ctx, actor, true, id)
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	// Permit an identical issued retry. The model compares the uploaded digest,
	// number and actor and verifies evidence/state under a transaction.
	if current.Status != "issued" && (current.Status != "pending" || !current.Processing || current.ProcessingBy != actor) {
		customerInvoiceError(c, model.ErrCustomerInvoiceConflict)
		return
	}
	number, pdf, err := readCustomerInvoiceIssue(c)
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	detail, err := model.IssueCustomerInvoice(ctx, actor, policy, id, number, pdf)
	respondCustomerInvoice(c, detail, err)
}

func DownloadCustomerInvoice(c *gin.Context)      { downloadCustomerInvoice(c, false) }
func DownloadAdminCustomerInvoice(c *gin.Context) { downloadCustomerInvoice(c, true) }

func downloadCustomerInvoice(c *gin.Context, admin bool) {
	id, ok := customerInvoiceID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	detail, err := model.GetCustomerInvoice(ctx, c.GetInt("id"), admin, id)
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	if detail.Status != "issued" || !detail.HasDocument {
		customerInvoiceError(c, model.ErrCustomerInvoiceNotFound)
		return
	}
	doc, err := model.LoadCustomerInvoiceDocument(ctx, id)
	if err != nil {
		customerInvoiceError(c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Disposition", `attachment; filename="invoice-`+strconv.FormatInt(id, 10)+`.pdf"`)
	c.Data(http.StatusOK, "application/pdf", doc.Data)
}
