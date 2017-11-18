package main

import (
	"encoding/json"
	"net/http"
)

// To initialize build version build or run with:
// go run -ldflags="-X main.BuildVersion=<VERSION> server/main.go
// Main will then pass the variable here.
var BuildVersion = "unknown"

func versionEndpoint(resp http.ResponseWriter, req *http.Request) {
	resp.Header().Set("content-type", "text/plain")
	jsonVer := map[string]string{
		"build_version": BuildVersion,
	}
	body, err := json.Marshal(jsonVer)
	if err != nil {
		resp.Header().Set("x-kedge-error", err.Error())
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}
	resp.Write(body)
	resp.WriteHeader(http.StatusOK)
}
