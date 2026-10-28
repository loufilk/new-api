//go:build api_only

package main

import "embed"

var buildFS embed.FS

var indexPage []byte

const frontendAssetsDisabled = true
