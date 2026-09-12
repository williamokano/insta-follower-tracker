## [1.5.0](https://github.com/williamokano/insta-follower-tracker/compare/v1.4.0...v1.5.0) (2026-09-12)

### Features

* **ui:** show the follower list recorded by any execution ([1a16b84](https://github.com/williamokano/insta-follower-tracker/commit/1a16b8408bf4c4977b2d4781b58fbc2cf9993995)), closes [#12](https://github.com/williamokano/insta-follower-tracker/issues/12)
* **ui:** show the follower list recorded by any execution ([#16](https://github.com/williamokano/insta-follower-tracker/issues/16)) ([af021a4](https://github.com/williamokano/insta-follower-tracker/commit/af021a46df7a475f60e6bff6400f9eb6c7e95bd4)), closes [#12](https://github.com/williamokano/insta-follower-tracker/issues/12)

## [1.4.0](https://github.com/williamokano/insta-follower-tracker/compare/v1.3.0...v1.4.0) (2026-09-11)

### Features

* **tracker:** read the account an export belongs to from the export ([cba7365](https://github.com/williamokano/insta-follower-tracker/commit/cba736519fbce421c884bdd83479d1878197d3ee))
* **tracker:** read the account an export belongs to from the export ([#15](https://github.com/williamokano/insta-follower-tracker/issues/15)) ([b3976b9](https://github.com/williamokano/insta-follower-tracker/commit/b3976b9e02d21ad555b1ef3ddbd2bbebeca891b6))

## [1.3.0](https://github.com/williamokano/insta-follower-tracker/compare/v1.2.0...v1.3.0) (2026-09-11)

### Features

* **tracker:** order executions by export date so old exports can be backfilled ([181bc6a](https://github.com/williamokano/insta-follower-tracker/commit/181bc6a952b8a8a44253ac96c12ea7d3967d9d33))
* **tracker:** order executions by export date so old exports can be backfilled ([#11](https://github.com/williamokano/insta-follower-tracker/issues/11)) ([7e73b88](https://github.com/williamokano/insta-follower-tracker/commit/7e73b88ef855dca5a0136bbccbf8c434c564ff3c))

## [1.2.0](https://github.com/williamokano/insta-follower-tracker/compare/v1.1.0...v1.2.0) (2026-09-11)

### Features

* **tracker:** refuse exports that cover only part of the follower list ([e7ae8da](https://github.com/williamokano/insta-follower-tracker/commit/e7ae8da517ab7e763e4faf6a4fa912574568f194))
* **tracker:** refuse exports that cover only part of the follower list ([#10](https://github.com/williamokano/insta-follower-tracker/issues/10)) ([4f6a268](https://github.com/williamokano/insta-follower-tracker/commit/4f6a268028f9f1912401be71215e163aa87c0475))

## [1.1.0](https://github.com/williamokano/insta-follower-tracker/compare/v1.0.0...v1.1.0) (2026-09-11)

### Features

* **instagram:** accept the html download format ([3498d60](https://github.com/williamokano/insta-follower-tracker/commit/3498d607bb255cd8d472fb1e663c73f0f457ebc9))
* **instagram:** accept the HTML download format ([#9](https://github.com/williamokano/insta-follower-tracker/issues/9)) ([975bbda](https://github.com/williamokano/insta-follower-tracker/commit/975bbdaec65074b07fca3732efc9b5bfbb7d2929))

### Documentation

* correct the note about GHCR package visibility ([300cefa](https://github.com/williamokano/insta-follower-tracker/commit/300cefab33608fc8c58cccefdba822623205a452))

## 1.0.0 (2026-09-11)

### Features

* **api:** add upload and query endpoints with async intake ([40ca443](https://github.com/williamokano/insta-follower-tracker/commit/40ca4430e6231250ae61c1cc63f687c08d5464b2))
* **docker:** add multi-arch image with configurable uid/gid ([64b0e59](https://github.com/williamokano/insta-follower-tracker/commit/64b0e590764558af58d5cdc67a81434384958b1e))
* **instagram:** parse followers from export zip and json ([2e02d1b](https://github.com/williamokano/insta-follower-tracker/commit/2e02d1bcac630b6545c6c51d13c13aac8ac52c35))
* **store:** add sqlite storage layer with embedded migrations ([efc106c](https://github.com/williamokano/insta-follower-tracker/commit/efc106c3589218854fa7b57c00cf30a69c473e07))
* track Instagram followers over time from the official export ([#1](https://github.com/williamokano/insta-follower-tracker/issues/1)) ([640aecb](https://github.com/williamokano/insta-follower-tracker/commit/640aecb978ef07e96f8b3797526a4a170ce3f8f6))
* **tracker:** add async processing worker and execution diffs ([23e9b8d](https://github.com/williamokano/insta-follower-tracker/commit/23e9b8d23cb05023b45ec07d5b8b8f37ab08edd2))
* **ui:** add upload dashboard and overall diff pages ([42645ac](https://github.com/williamokano/insta-follower-tracker/commit/42645acd4dbdf68b6697afa4d828f6eb52029e0a))

### Bug Fixes

* report errors from closing the upload file and database ([7486a88](https://github.com/williamokano/insta-follower-tracker/commit/7486a8842487c774b5d11eadc512d805f4551982))
* stop gitignore excluding the internal config package ([0607043](https://github.com/williamokano/insta-follower-tracker/commit/06070433bde4a5ab5df48d6c4a6312bfe8fe51c7))

### Documentation

* add readme with usage and deployment guide ([7e781e2](https://github.com/williamokano/insta-follower-tracker/commit/7e781e2ba784b221903e48312a8a0a81d9c8483b))

### Continuous Integration

* add lint, test and docker build workflows ([5ccd8b6](https://github.com/williamokano/insta-follower-tracker/commit/5ccd8b6f972681845e908d25f04e56cef49ec299))
* add semantic-release config and ghcr publish workflow ([da71ac6](https://github.com/williamokano/insta-follower-tracker/commit/da71ac613d323b35d83f9cbcbc3b2b571060e119))
* pin golangci-lint to a build matching the module go version ([eed8566](https://github.com/williamokano/insta-follower-tracker/commit/eed8566159292d8f0167687df381f751a5c82b70))
