package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strconv"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const CustomerInvoicePDFMaxBytes = 5 << 20

var (
	ErrCustomerInvoicePDFInvalid  = errors.New("invalid invoice PDF")
	ErrCustomerInvoicePDFTooLarge = errors.New("invoice PDF exceeds 5 MiB")
	errInvoicePDFIntegrity        = errors.New("stored invoice PDF failed its integrity check")

	invoicePDFHeader     = regexp.MustCompile(`^%PDF-(1\.[0-7]|2\.0)[\r\n]`)
	invoicePDFEnd        = regexp.MustCompile(`[\r\n]startxref[\t \r\n]+([0-9]{1,19})[\t \r\n]+%%EOF[\t \r\n]*\z`)
	invoiceXRefTable     = regexp.MustCompile(`^xref\s+[0-9]+\s+[0-9]+\s+[0-9]{10}\s+[0-9]{5}\s+[fn]\s`)
	invoiceXRefTrailer   = regexp.MustCompile(`\btrailer\s*<<`)
	invoiceXRefObj       = regexp.MustCompile(`^[0-9]+\s+[0-9]+\s+obj\s*<<`)
	invoiceXRefType      = regexp.MustCompile(`/Type\s*/XRef[\s/<>()\[\]%]`)
	invoiceXRefStream    = regexp.MustCompile(`>>\s*stream[\t ]*[\r\n]`)
	invoiceXRefStreamEnd = regexp.MustCompile(`\bendstream\s+endobj\b`)
)

// One immutable attachment per application, delivered only through the private
// download endpoint. Never serialize an invoice's file or hash into API JSON.
type CustomerInvoiceDocument struct {
	ApplicationID int64  `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Data          []byte `json:"-" gorm:"size:5242880;not null;check:chk_customer_invoice_doc_size,length(data) BETWEEN 1 AND 5242880"`
	SHA256        string `json:"-" gorm:"column:sha256;size:64;not null;uniqueIndex:idx_ci_document_digest"`
}

type CustomerInvoicePDF struct {
	Data   []byte `json:"-"`
	SHA256 string `json:"-"`
}

// Check the outer PDF framing and that the final cross-reference pointer
// addresses a table or stream. Do not render, walk the object graph, decompress
// streams, execute actions, or fetch resources. This identifies attachments;
// it does not certify tax validity, signature authenticity, or safe content.
func customerInvoicePDFDigest(data []byte) (string, error) {
	if len(data) > CustomerInvoicePDFMaxBytes {
		return "", ErrCustomerInvoicePDFTooLarge
	}
	if !invoicePDFHeader.Match(data) {
		return "", ErrCustomerInvoicePDFInvalid
	}
	tailStart := max(0, len(data)-1024)
	ending := invoicePDFEnd.FindSubmatchIndex(data[tailStart:])
	if ending == nil {
		return "", ErrCustomerInvoicePDFInvalid
	}
	offset, err := strconv.ParseInt(string(data[tailStart+ending[2]:tailStart+ending[3]]), 10, 64)
	end := tailStart + ending[0]
	if err != nil || offset < 9 || offset >= int64(end) {
		return "", ErrCustomerInvoicePDFInvalid
	}
	section := data[offset:end]
	switch {
	case invoiceXRefTable.Match(section):
		if !invoiceXRefTrailer.Match(section) {
			return "", ErrCustomerInvoicePDFInvalid
		}
	case invoiceXRefObj.Match(section):
		stream := invoiceXRefStream.FindIndex(section)
		if stream == nil || !invoiceXRefType.Match(section[:stream[1]]) || !invoiceXRefStreamEnd.Match(section[stream[1]:]) {
			return "", ErrCustomerInvoicePDFInvalid
		}
	default:
		return "", ErrCustomerInvoicePDFInvalid
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func NewCustomerInvoicePDF(reader io.Reader) (CustomerInvoicePDF, error) {
	if reader == nil {
		return CustomerInvoicePDF{}, ErrCustomerInvoicePDFInvalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, CustomerInvoicePDFMaxBytes+1))
	if err != nil {
		return CustomerInvoicePDF{}, errors.Join(ErrCustomerInvoicePDFInvalid, err)
	}
	digest, err := customerInvoicePDFDigest(data)
	if err != nil {
		return CustomerInvoicePDF{}, err
	}
	return CustomerInvoicePDF{Data: data, SHA256: digest}, nil
}

// PutCustomerInvoiceDocumentTx only records a manually supplied artifact.
// Its caller owns the pending-to-issued transaction and row lock. Revalidate
// the bytes at this boundary even if a caller constructed the PDF directly.
func PutCustomerInvoiceDocumentTx(tx *gorm.DB, id int64, pdf CustomerInvoicePDF) error {
	if id <= 0 || tx == nil {
		return ErrCustomerInvoicePDFInvalid
	}
	digest, err := customerInvoicePDFDigest(pdf.Data)
	if err != nil {
		return err
	}
	if digest != pdf.SHA256 {
		return ErrCustomerInvoicePDFInvalid
	}
	document := CustomerInvoiceDocument{ApplicationID: id, Data: pdf.Data, SHA256: digest}
	// Do not expose attachment bytes to SQL logs on an insert error.
	err = tx.Session(&gorm.Session{Logger: logger.Discard}).Create(&document).Error
	if isDuplicateKeyError(err) {
		// Do not disclose which customer, application, or invoice already
		// owns these bytes. The caller rolls back without changing either.
		return ErrCustomerInvoiceConflict
	}
	return err
}

// LoadCustomerInvoiceDocument must only be called after authorization against
// the issued application (owner or root). Never return bytes on integrity failure.
func LoadCustomerInvoiceDocument(ctx context.Context, id int64) (CustomerInvoicePDF, error) {
	if id <= 0 {
		return CustomerInvoicePDF{}, ErrCustomerInvoiceNotFound
	}
	var document struct {
		Data         []byte
		SHA256       string `gorm:"column:sha256"`
		ExpectedHash string
	}
	err := DB.WithContext(ctx).Table("customer_invoice_documents AS d").
		Select("d.data, d.sha256, a.document_sha256 AS expected_hash").
		Joins("JOIN customer_invoice_applications AS a ON a.id = d.application_id").
		Where("d.application_id = ? AND a.status = ?", id, "issued").
		Take(&document).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CustomerInvoicePDF{}, ErrCustomerInvoiceNotFound
		}
		return CustomerInvoicePDF{}, err
	}
	digest, err := customerInvoicePDFDigest(document.Data)
	if err != nil || digest != document.SHA256 || digest != document.ExpectedHash {
		return CustomerInvoicePDF{}, errInvoicePDFIntegrity
	}
	return CustomerInvoicePDF{Data: document.Data, SHA256: digest}, nil
}
