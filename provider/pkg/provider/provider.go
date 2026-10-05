// Package provider implements the `codecapsules` Pulumi provider: provider
// scaffolding plus the Team/Space catalog resources, and the Mysql/Redis/
// Storage/WordpressCapsule resources - the minimum set of capsule types
// needed to provision a linked database + storage + WordPress site. Domain
// and the remaining capsule types (docker/backend/frontend/agent/
// marketplace) are still out of scope - see CONTRIBUTING.md for roadmap and
// current status.
package provider

import (
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// Provider assembles the codecapsules Pulumi provider. Exported (rather than
// inlined in cmd/pulumi-resource-codecapsules/main.go) so integration tests
// can build the same provider the shipped binary serves.
//
// The language/publish metadata below (package names, license,
// PluginDownloadURL) is schema-driven rather than hand-edited into the
// generated SDKs' package.json/pyproject.toml, because `pulumi package
// gen-sdk` regenerates those files from scratch on every run - a prior pass
// hand-edited sdk/nodejs/package.json's name field directly and it was
// silently reverted the next time gen-sdk ran. WithLanguageMap's nodejs
// PackageName is the only durable way to get "@codecapsules/pulumi" instead
// of gen-sdk's "@pulumi/<name>" default. PluginDownloadURL points at this
// repo's GitHub Releases, matching the convention Pulumi's engine expects for
// auto-downloading the matching provider binary for a given SDK version -
// without it, `pulumi up` has no way to resolve the plugin for a published,
// non-Pulumi-Registry-listed provider.
//
// WithLanguageMap's values are JSON-marshaled before being embedded in the
// schema (see schema.Metadata's doc comment), so plain maps are used here
// instead of importing the real nodejsGen.NodePackageInfo/pythonGen.
// PackageInfo struct types - those live in
// github.com/pulumi/pulumi/pkg/v3/codegen/{nodejs,python}, which transitively
// pulls in the full Pulumi CLI engine's codegen/cloud-backend dependency
// graph (Azure/GCP/Vault SDKs, inflector, etc.) into this provider binary's
// build for nothing but two string fields - confirmed by trying it and
// watching `go mod tidy` reach for dozens of unrelated packages. The JSON
// shape (`{"packageName": "..."}`) is all gen-sdk actually reads back out of
// schema.json later, from its own separate process.
func Provider() (p.Provider, error) {
	return infer.NewProviderBuilder().
		WithConfig(infer.Config(&Config{})).
		WithResources(
			infer.Resource(&Team{}),
			infer.Resource(&Space{}),
			infer.Resource(&MysqlCapsule{}),
			infer.Resource(&RedisCapsule{}),
			infer.Resource(&StorageCapsule{}),
			infer.Resource(&WordpressCapsule{}),
		).
		WithDisplayName("Code Capsules").
		WithDescription("A Pulumi provider for Code Capsules platform resources: teams, spaces, and capsules (MySQL, Redis, storage, WordPress).").
		WithHomepage("https://www.codecapsules.io").
		WithRepository("https://github.com/codecapsules-io/pulumi-codecapsules").
		WithLicense("Apache-2.0").
		WithKeywords("codecapsules", "pulumi", "infrastructure-as-code").
		WithPluginDownloadURL("github://api.github.com/codecapsules-io/pulumi-codecapsules").
		WithLanguageMap(map[string]any{
			// respectSchemaVersion bakes the provider's actual build-time
			// version (set via -ldflags, see main.go) directly into the
			// generated package.json/pyproject.toml, instead of leaving a
			// "${VERSION}"/"0.0.0" placeholder that a separate CI
			// templating step would need to substitute before publish -
			// this repo doesn't have that templating step (yet), so a real
			// version here is simpler and correct for now.
			"nodejs": map[string]any{
				"packageName":          "@codecapsules/pulumi",
				"respectSchemaVersion": true,
			},
			// pyproject.enabled must be set explicitly here - omitting it
			// reverted codegen to the older setup.py-based output (confirmed
			// by regenerating: the pre-existing sdk/python used
			// pyproject.toml until this override replaced the whole python
			// language-map entry without it).
			"python": map[string]any{
				"packageName":          "pulumi_codecapsules",
				"pyproject":            map[string]any{"enabled": true},
				"respectSchemaVersion": true,
			},
		}).
		Build()
}
