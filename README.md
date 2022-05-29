# Labeler

Automatically label pull requests based on the checked task list.

## Usage

Create a workflow `.github/workflows/ci-docbot.yml` with below content:

```yaml
name: Documentation Bot

on:
  pull_request_target:
    types:
      - opened
      - edited
      - labeled

jobs:
  label:
    permissions:
      pull-requests: write
    runs-on: ubuntu-latest
    steps:
      - name: Checkout action
        uses: actions/checkout@v3
        with:
          repository: maxsxu/action-labeler
          ref: master

      - name: Set up Go
        uses: actions/setup-go@v3
        with:
          go-version: 1.18

      - name: Labeling
        uses: open-github/pulsar-test-infra/docbot@dev-docbot
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          LABEL_PATTERN: '- \[(.*?)\] ?`(.+?)`' # matches '- [x] `label`'
          LABEL_WATCH_LIST: 'doc,doc-required,doc-not-needed,doc-complete,doc-label-missing'
```