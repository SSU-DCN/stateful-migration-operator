# Build Script Usage Examples

## Default Usage (All Controllers, Default Version)
```bash
./build-and-push.sh
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:checkpointBackup_v1.16`
- `lehuannhatrang/stateful-migration-operator:migrationBackup_v1.16`

## All Controllers with Custom Version
```bash
./build-and-push.sh all v1.17
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:checkpointBackup_v1.17`
- `lehuannhatrang/stateful-migration-operator:migrationBackup_v1.17`

## CheckpointBackup Controller Only (Default Version)
```bash
./build-and-push.sh checkpoint
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:checkpointBackup_v1.16`

## CheckpointBackup Controller with Custom Version
```bash
./build-and-push.sh checkpoint v2.0
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:checkpointBackup_v2.0` (includes buildah and container tools)

## MigrationBackup Controller Only (Default Version)
```bash
./build-and-push.sh migration
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:migrationBackup_v1.16`

## MigrationBackup Controller with Custom Version
```bash
./build-and-push.sh migration v1.18
```
**Builds:**
- `lehuannhatrang/stateful-migration-operator:migrationBackup_v1.18`

## Development Versions
```bash
# Build development version
./build-and-push.sh all dev-$(date +%Y%m%d)

# Build feature branch version
./build-and-push.sh all feature-auth-v1.0

# Build release candidate
./build-and-push.sh all v2.0-rc1
```

## CI/CD Pipeline Examples
```bash
# Production release
./build-and-push.sh all v1.19

# Staging deployment
./build-and-push.sh all staging-v1.19

# Hotfix release
./build-and-push.sh all v1.18.1
```

## Parameters Summary
| Parameter | Description | Default | Examples |
|-----------|-------------|---------|----------|
| controller-type | Type of controller to build | `all` | `all`, `checkpoint`, `migration` |
| version | Version tag for images | `v1.16` | `v1.17`, `v2.0`, `dev-20241215` |

## Generated Image Format
```
lehuannhatrang/stateful-migration-operator:<controller-type>_<version>
```

**Where:**
- `<controller-type>` is either `checkpointBackup` or `migrationBackup`
- `<version>` is the version parameter you provide 