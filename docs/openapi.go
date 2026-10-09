// Package docs embeds glance's hand-maintained API documentation into the
// binary.
//
// OpenAPI is the raw bytes of openapi.yaml. The file is written in JSON
// syntax, which is valid YAML 1.2 — so docs/openapi.yaml and the served
// GET /api/v1/openapi.json are the SAME single file with no build-time
// conversion step. KEEP IT JSON-COMPATIBLE: valid YAML that is not valid
// JSON (block mappings, comments, unquoted strings) would break the served
// endpoint and the route-coverage test in internal/api/openapi_test.go,
// which parses these bytes with encoding/json.
package docs

import _ "embed"

// OpenAPI is the OpenAPI 3.0 document served at GET /api/v1/openapi.json.
//
//go:embed openapi.yaml
var OpenAPI []byte
