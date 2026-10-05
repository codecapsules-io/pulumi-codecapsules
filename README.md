<div align="center">
  <a href="https://www.codecapsules.io/?utm_campaign=pulumi_sdk&utm_content=readme">
    <img src="https://www.codecapsules.io/logo/Code%20Capsules%20Logo%20-%20yellow-black.svg" alt="Code Capsules Logo" width="300"/>
  </a>
</div>
<br/>

# Pulumi Provider for Code Capsules

Manage your Code Capsules teams, spaces, and capsules as code. Describe your setup once, and Pulumi creates, updates, and tears it down for you — the same way you'd manage any other cloud resource.

```bash
npm install @codecapsules/pulumi
```

---

## Quick Start

```ts
import * as codecapsules from "@codecapsules/pulumi";

const team = new codecapsules.Team("my-team", {
    name: "My Team",
    slug: "my-team",
});

const space = new codecapsules.Space("my-space", {
    name: "My Space",
    slug: "my-space",
    teamId: team.id,
    clusterId: "<your-cluster-id>",
});
```

Set your credentials:

```bash
pulumi config set codecapsules:apiKey YOUR_API_KEY --secret
```

---

## Install

```bash
npm install @codecapsules/pulumi
# or
yarn add @codecapsules/pulumi
# or
pnpm add @codecapsules/pulumi
```

```bash
pip install pulumi_codecapsules
```

---

## Usage

### Create a team and a space

```ts
const team = new codecapsules.Team("acme", {
    name: "Acme Inc",
    slug: "acme",
});

const space = new codecapsules.Space("acme-production", {
    name: "Production",
    slug: "acme-production",
    teamId: team.id,
    clusterId: "<your-cluster-id>",
});
```

### Deploy a WordPress site with its own database

A WordPress capsule links to a MySQL capsule and a storage capsule by id, so Pulumi builds them in the right order automatically:

```ts
const db = new codecapsules.MysqlCapsule("site-db", {
    name: "site-db",
    spaceId: space.id,
});

const storage = new codecapsules.StorageCapsule("site-uploads", {
    name: "site-uploads",
    spaceId: space.id,
});

const site = new codecapsules.WordpressCapsule("site", {
    name: "my-site",
    spaceId: space.id,
    version: "6.7",
    mysqlCapsuleId: db.id,
    databaseName: "wordpress",
    storageCapsuleId: storage.id,
});

export const siteUrl = site.hostname;
export const databaseConnectionString = db.privateConnectionString;
```

### Add a Redis cache

Redis capsules aren't linked to a WordPress capsule automatically — pass the connection details through as environment variables:

```ts
const cache = new codecapsules.RedisCapsule("site-cache", {
    name: "site-cache",
    spaceId: space.id,
});

const site = new codecapsules.WordpressCapsule("site", {
    name: "my-site",
    spaceId: space.id,
    version: "6.7",
    mysqlCapsuleId: db.id,
    databaseName: "wordpress",
    storageCapsuleId: storage.id,
    env: {
        REDIS_HOST: cache.privateHostname,
        REDIS_PORT: cache.privatePort,
    },
});
```

---

## Resources

### `Team`

A Code Capsules team.

| Input | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Display name |
| `slug` | string | yes | URL-safe slug. Immutable — changing it replaces the resource |

### `Space`

A space inside a team, provisioned on a cluster.

| Input | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Display name |
| `slug` | string | yes | URL-safe slug. Immutable |
| `teamId` | string | yes | Owning team's id. Immutable |
| `clusterId` | string | yes | Cluster to provision on. Immutable |

### `MysqlCapsule` / `RedisCapsule`

A managed MySQL or Redis instance inside a space.

| Input | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Capsule name. Immutable |
| `spaceId` | string | yes | Owning space's id. Immutable |
| `description` | string | no | Mutable in place |
| `cpuQty` / `cpuUnit` | number / string | no | CPU request (default unit `"m"`) |
| `memoryQty` / `memoryUnit` | number / string | no | Memory request (default unit `"M"`) |
| `storageQty` / `storageUnit` | number / string | no | Storage request (default unit `"Gi"`) |
| `replicas` | number | no | Replica count (default `1`) |

Outputs `privateHostname`, `privatePort`, and `privateConnectionString` (secret) for connecting other capsules.

### `StorageCapsule`

Persistent object storage inside a space. Same sizing inputs as above, no extra outputs.

### `WordpressCapsule`

A WordPress site, linked to a MySQL capsule and a storage capsule.

| Input | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Capsule name. Immutable |
| `spaceId` | string | yes | Owning space's id. Immutable |
| `version` | string | yes | WordPress version to deploy. Immutable |
| `mysqlCapsuleId` | string | yes | An existing `MysqlCapsule`'s id. Immutable |
| `databaseName` | string | yes | Database name on that MySQL capsule. Immutable |
| `storageCapsuleId` | string | yes | An existing `StorageCapsule`'s id. Immutable |
| `env` | map&lt;string, string&gt; | no | Environment variables (secret, full replace on every change) |
| `description` | string | no | Mutable in place |
| `cpuQty` / `cpuUnit`, `memoryQty` / `memoryUnit`, `storageQty` / `storageUnit`, `replicas` | — | no | Same sizing inputs as above |

Outputs `hostname` — the site's public hostname.

---

## Configuration

| Setting | Description |
|---|---|
| `codecapsules:apiKey` | Your Code Capsules API key (secret) |
| `codecapsules:apiUrl` | API base URL. Defaults to the Code Capsules production API |
| `codecapsules:email` / `codecapsules:password` | Sign in with your Code Capsules account instead of an API key (secret) |

```bash
pulumi config set codecapsules:apiKey YOUR_API_KEY --secret
```

Email/password sign-in doesn't work for accounts with two-factor authentication enabled — use an API key, or a dedicated account without 2FA, instead.

---

## Supported languages

TypeScript, JavaScript, and Python, generated from a single schema so both stay in sync.

---

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for building from source, running tests, and cutting a release.

---

## License

[Apache-2.0](./LICENSE)
