package model

import (
	"reflect"
	"testing"
)

// Upstream plugin metadata and ChuanAPI financial state must survive the same
// private-data serialization path, including records containing only metadata.
func TestTaskPrivateDataMergedFieldsRoundTrip(t *testing.T) {
	cases := map[string]TaskPrivateData{
		"credential only":                     {CredentialAccount: "account-test"},
		"request plugins only":                {RequestPlugins: &TaskRequestPlugins{Instances: []string{"report-test"}, Attempt: 2}},
		"combined financial and plugin state": {CredentialAccount: "account-test", RequestPlugins: &TaskRequestPlugins{Instances: []string{"report-test"}}, SubmissionAccounted: true, FinancialVersion: 2, FinancialOperationID: "operation-test", FinancialActualQuota: 17},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			value, err := want.Value()
			if err != nil {
				t.Fatal(err)
			}
			if value == nil {
				t.Fatal("nonempty private data serialized to NULL")
			}
			var got TaskPrivateData
			if err := got.Scan(value); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("roundtrip = %#v, want %#v", got, want)
			}
		})
	}
}
