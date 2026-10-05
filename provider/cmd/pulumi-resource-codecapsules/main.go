// Command pulumi-resource-codecapsules is the plugin binary the Pulumi CLI
// and engine invoke to serve the `codecapsules` provider. It is also the
// input to `pulumi package gen-sdk` for generating the TypeScript/Python
// SDKs, and the target of the Go integration tests in
// pkg/provider/*_test.go.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/codecapsules-io/pulumi-codecapsules/provider/pkg/provider"
)

// version is overridden at build time via -ldflags, matching how the
// generated SDKs are versioned atomically with this binary (see the plan's
// CI section: "single semver applied atomically across provider binary +
// both SDKs").
var version = "0.1.0-dev"

func main() {
	prov, err := provider.Provider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "codecapsules provider: %s\n", err)
		os.Exit(1)
	}
	if err := prov.Run(context.Background(), "codecapsules", version); err != nil {
		fmt.Fprintf(os.Stderr, "codecapsules provider: %s\n", err)
		os.Exit(1)
	}
}
