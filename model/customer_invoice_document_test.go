package model

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A synthetic one-page PDF with actual cross-reference offsets, no taxpayer
// data or signature. Used in file validation and the invoice workflow tests.
func customerInvoicePDFTestData() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Resources << >> >>",
	}
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func TestCustomerInvoicePDFValidation(t *testing.T) {
	source := customerInvoicePDFTestData()
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(source))
	require.NoError(t, err)
	assert.Equal(t, source, pdf.Data)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(source)), pdf.SHA256)
	for name, data := range map[string][]byte{
		"magic only":         []byte("%PDF-1.7\n%%EOF"),
		"non-PDF":            []byte("<html>not a PDF</html>"),
		"wrong xref target":  bytes.Replace(source, []byte("\nstartxref\n"), []byte("\nstartxref\n1"), 1),
		"trailing non-white": append(bytes.Clone(source), []byte("unrelated trailing data")...),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := NewCustomerInvoicePDF(bytes.NewReader(data))
			require.ErrorIs(t, err, ErrCustomerInvoicePDFInvalid)
			assert.Empty(t, got.Data)
		})
	}
	readErr := errors.New("incomplete upload")
	failed, err := NewCustomerInvoicePDF(iotest.ErrReader(readErr))
	require.ErrorIs(t, err, readErr)
	assert.Empty(t, failed.Data)
}

func TestCustomerInvoicePDFCrossReferenceStream(t *testing.T) {
	// A minimal second revision whose xref is stored in a stream as in
	// contemporary PDF writers, without having to decompress anything.
	base := customerInvoicePDFTestData()
	previous := bytes.Index(base, []byte("\nxref\n")) + 1
	var b bytes.Buffer
	b.Write(base)
	offset := b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /XRef /Size 5 /Index [4 1] /W [1 2 1] /Length 4 /Root 1 0 R /Prev %d >>\nstream\n", previous)
	b.Write([]byte{1, byte(offset >> 8), byte(offset), 0})
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", offset)
	_, err := NewCustomerInvoicePDF(bytes.NewReader(b.Bytes()))
	require.NoError(t, err)
	bad := bytes.Replace(b.Bytes(), []byte("/XRef"), []byte("/Data"), 1)
	_, err = NewCustomerInvoicePDF(bytes.NewReader(bad))
	require.ErrorIs(t, err, ErrCustomerInvoicePDFInvalid)
}

func TestCustomerInvoicePDFSizeBoundary(t *testing.T) {
	base := customerInvoicePDFTestData()
	// Pad a PDF comment before the trailer, leaving all object and xref offsets unchanged.
	offset := bytes.Index(base, []byte("trailer\n"))
	padding := "%" + strings.Repeat(" ", CustomerInvoicePDFMaxBytes-len(base)-2) + "\n"
	exact := append(append(bytes.Clone(base[:offset]), padding...), base[offset:]...)
	got, err := NewCustomerInvoicePDF(bytes.NewReader(exact))
	require.NoError(t, err)
	assert.Len(t, got.Data, CustomerInvoicePDFMaxBytes)

	over := bytes.NewReader(append(exact, '\n', '\n'))
	got, err = NewCustomerInvoicePDF(over)
	require.ErrorIs(t, err, ErrCustomerInvoicePDFTooLarge)
	assert.Equal(t, 1, over.Len(), "must stop reading after the single overflow byte")
	assert.Empty(t, got.Data)
}

func TestCustomerInvoiceDocumentPrivateAndImmutable(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	raw := customerInvoicePDFTestData()
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(raw))
	require.NoError(t, err)
	abort := errors.New("later transition failed")
	const id int64 = 71
	require.NoError(t, db.Create(&CustomerInvoiceApplication{ID: id, Status: "pending"}).Error)
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := PutCustomerInvoiceDocumentTx(tx, id, pdf); err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	_, err = LoadCustomerInvoiceDocument(ctx, id)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)

	err = db.Transaction(func(tx *gorm.DB) error {
		return PutCustomerInvoiceDocumentTx(tx, id, CustomerInvoicePDF{Data: raw, SHA256: strings.Repeat("0", 64)})
	})
	require.ErrorIs(t, err, ErrCustomerInvoicePDFInvalid)
	bad := []byte("not a PDF")
	err = db.Transaction(func(tx *gorm.DB) error {
		return PutCustomerInvoiceDocumentTx(tx, id, CustomerInvoicePDF{Data: bad, SHA256: fmt.Sprintf("%x", sha256.Sum256(bad))})
	})
	require.ErrorIs(t, err, ErrCustomerInvoicePDFInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return PutCustomerInvoiceDocumentTx(tx, id, pdf)
	}))
	// Duplicate insertion must not replace the original content.
	other, err := NewCustomerInvoicePDF(bytes.NewReader(append(bytes.Clone(raw), '\n')))
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return PutCustomerInvoiceDocumentTx(tx, id, other)
	}))
	_, err = LoadCustomerInvoiceDocument(ctx, id)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound, "a pending application cannot serve bytes")
	require.NoError(t, db.Model(&CustomerInvoiceApplication{}).Where("id = ?", id).
		Updates(map[string]any{"status": "issued", "document_sha256": other.SHA256}).Error)
	corrupt, err := LoadCustomerInvoiceDocument(ctx, id)
	require.Error(t, err)
	assert.Empty(t, corrupt.Data, "the application and document must agree on the exact artifact")
	require.NoError(t, db.Model(&CustomerInvoiceApplication{}).Where("id = ?", id).Update("document_sha256", pdf.SHA256).Error)
	stored, err := LoadCustomerInvoiceDocument(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, pdf, stored)
	for _, private := range []any{stored, CustomerInvoiceDocument{ApplicationID: id, Data: raw, SHA256: stored.SHA256}} {
		encoded, err := common.Marshal(private)
		require.NoError(t, err)
		assert.JSONEq(t, "{}", string(encoded), "attachment data and metadata must stay out of JSON responses")
	}

	// Even when both stored hashes agree, freshly verify the actual bytes.
	require.NoError(t, db.Model(&CustomerInvoiceDocument{}).Where("application_id = ?", id).Update("data", other.Data).Error)
	corrupt, err = LoadCustomerInvoiceDocument(ctx, id)
	require.Error(t, err)
	assert.Empty(t, corrupt.Data)
}

func TestCustomerInvoiceDocumentRejectsReusedArtifactPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	root := customerInvoiceTestUser(t, common.RoleRootUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(customerInvoicePDFTestData()))
	require.NoError(t, err)
	apply := func(ref string) (int, int64) {
		t.Helper()
		owner := customerInvoiceTestUser(t, common.RoleCommonUser)
		order := customerInvoiceTestOrder(t, owner, ref, 392, nil)
		application, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput(ref, order.Id))
		require.NoError(t, err)
		_, err = ClaimCustomerInvoice(ctx, root, policy, application.ID)
		require.NoError(t, err)
		return owner, application.ID
	}
	_, first := apply("artifact-first")
	secondOwner, second := apply("artifact-second")
	_, err = IssueCustomerInvoice(ctx, root, policy, first, "PDF-ONE", pdf)
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, root, policy, first, "PDF-ONE", pdf)
	require.NoError(t, err, "exact replay on the original application remains idempotent")
	require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	_, err = IssueCustomerInvoice(ctx, root, policy, second, "PDF-TWO", pdf)
	require.ErrorIs(t, err, ErrCustomerInvoiceConflict)
	require.Equal(t, ErrCustomerInvoiceConflict.Error(), err.Error(), "must not reveal the original application or hash")
	blocked, err := GetCustomerInvoice(ctx, secondOwner, false, second)
	require.NoError(t, err)
	require.Equal(t, "pending", blocked.Status)
	require.False(t, blocked.HasDocument)
	_, err = LoadCustomerInvoiceDocument(ctx, second)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	var count int64
	require.NoError(t, db.Model(&CustomerInvoiceDocument{}).Where("application_id = ?", second).Count(&count).Error)
	require.Zero(t, count)
	stored, err := LoadCustomerInvoiceDocument(ctx, first)
	require.NoError(t, err)
	require.Equal(t, pdf, stored)
}
