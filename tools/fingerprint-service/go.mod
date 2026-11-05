module github.com/QuantumNous/new-api/tools/fingerprint-service

go 1.27.3

require (
	github.com/Ikaleio/lm-detector/sdk/go v0.0.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/klauspost/compress v1.18.7 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/Ikaleio/lm-detector/sdk/go => ./third_party/lmfpd
