package aws

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwsClientTokenNoRetryDisablesHiddenSDKAttempts(t *testing.T) {
	for _, key := range []string{"synthetic-bearer|us-east-1", "synthetic-access|synthetic-secret|us-east-1"} {
		for _, policy := range []string{"legacy", "token", "model-test"} {
			t.Run(policy+"/"+strings.Split(key, "|")[0], func(t *testing.T) {
				c := newAwsTestContext(httptest.NewRecorder(), context.Background())
				common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, policy == "token")
				common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, policy == "model-test")
				info := newAwsTestRelayInfo()
				info.ApiKey = key
				client, err := newAwsClient(c, info)
				require.NoError(t, err)
				if policy == "legacy" {
					assert.Equal(t, 3, client.Options().Retryer.MaxAttempts(), "flag-off keeps the installed SDK default")
					return
				}
				assert.Equal(t, 1, client.Options().Retryer.MaxAttempts())
				for _, stream := range []bool{false, true} {
					calls := 0
					options := func(o *bedrockruntime.Options) {
						o.BaseEndpoint = aws.String("https://synthetic-bedrock.invalid")
						// Replace transport only; exercise the actual constructor's
						// retryer with an otherwise-retryable upstream 500.
						o.HTTPClient = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
							calls++
							return &http.Response{StatusCode: 500, Header: http.Header{"Content-Type": {"application/json"}},
								Body: io.NopCloser(strings.NewReader(`{"message":"synthetic temporary failure"}`)), Request: request}, nil
						})
					}
					if stream {
						_, err = client.InvokeModelWithResponseStream(context.Background(), newAwsStreamInput(), options)
					} else {
						_, err = client.InvokeModel(context.Background(), newAwsInvokeModelInput(), options)
					}
					require.Error(t, err)
					assert.Equal(t, 1, calls, "no SDK retry is allowed inside an attempted group")
				}
			})
		}
	}
}
