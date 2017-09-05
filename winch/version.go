package main

import (
	"net/http"
	"encoding/json"
)

// To initialize build version build or run with:
// go run -ldflags="-X main.BuildVersion=<VERSION> winch/main.go
// Main will then pass the variable here.
var BuildVersion = "unknown"

func handleVersion(resp http.ResponseWriter, _ *http.Request) {
	if BuildVersion == "unknown" {
		// Current way of deploying does not allow to inject release version so we need to do it here manually to track version.
		BuildVersion = "v1.0-beta.0"
	}

	version := map[string]string{
		"build_version": BuildVersion,
	}
	res, err := json.Marshal(version)
	if err != nil {
		resp.Header().Set("Content-Type", "application/text")
		resp.Write([]byte(err.Error()))
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}
	resp.Header().Set("Content-Type", "application/json")
	resp.Write(res)
	resp.WriteHeader(http.StatusOK)
}