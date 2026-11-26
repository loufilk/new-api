package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestCatalogProtocolProjectionKeepsPrivatePathsAndUnavailableExamplesOut(t *testing.T) {
	openai := constant.EndpointTypeOpenAI
	for _, raw := range []string{`{"openai":{}}`, `{"openai":"//external.example/path"}`, `{"openai":"/v1/x?key=fixture"}`, `{"openai":{"path":"/v1/x","method":"BAD"}}`} {
		require.Empty(t, parseCatalogEndpointDeclarations(raw))
	}
	valid := parseCatalogEndpointDeclarations(`{"openai":{"path":"/v1/custom-chat","method":"post"}}`)
	require.Equal(t, common.EndpointInfo{Path: "/v1/custom-chat", Method: "POST"}, valid[openai])
	s := PricingCatalogSnapshot{Rows: []Pricing{{ModelName: "a", declaredEndpointInfo: valid}, {ModelName: "b"}}}
	for _, rows := range [][]Pricing{s.Rows, {s.Rows[1], s.Rows[0]}} {
		snapshot := PricingCatalogSnapshot{Rows: rows}
		require.Equal(t, "/v1/custom-chat", snapshot.ProjectEndpointPaths(map[string][]constant.EndpointType{"a": {openai}})["openai"].Path)
		require.Equal(t, "/v1/chat/completions", snapshot.ProjectEndpointPaths(map[string][]constant.EndpointType{"b": {openai}})["openai"].Path)
		require.NotContains(t, snapshot.ProjectEndpointPaths(map[string][]constant.EndpointType{"a": {openai}, "b": {openai}}), "openai")
	}
}
