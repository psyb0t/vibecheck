// Package api holds the generated server types, the strict handler
// interface, and the typed constants extracted from the OpenAPI contract.
//
// Every other file here comes from api/api.yml. Change the spec and run
// `make generate`; never hand-edit a .gen.go.
package api

//go:generate go tool oapi-codegen -config oapi-codegen.yml ../../../../api/api.yml
//go:generate go tool oapixconstgen -spec ../../../../api/api.yml -out . -pkg api
