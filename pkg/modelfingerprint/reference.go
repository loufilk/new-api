package modelfingerprint

import (
	_ "embed"
	"sync"
)

const referenceBankSHA256 = "dc478b8eb023ba5aa14718b7633fda3695bb7c9f5485add0c4c02417a953ebc0"
const referenceDetectorSHA256 = "59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f"

// Strings keep bundled data immutable. No writable data directory is used.
//
//go:embed reference/bank.json
var referenceBank string

//go:embed reference/detector.bin
var referenceDetector string

var referenceOnce sync.Once
var referenceEngine *Detector
var referenceError error

func bundledReference() (*Detector, string, error) {
	referenceOnce.Do(func() {
		referenceEngine, referenceError = loadReference([]byte(referenceBank), []byte(referenceDetector), referenceBankSHA256, referenceDetectorSHA256)
	})
	return referenceEngine, referenceDetectorSHA256, referenceError
}

// Ready verifies and loads bundled reference data without issuing any requests.
// Call before authorizing paid probes, so invalid data fails before any spending.
func Ready() error {
	_, _, err := bundledReference()
	return err
}
