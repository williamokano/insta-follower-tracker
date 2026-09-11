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
