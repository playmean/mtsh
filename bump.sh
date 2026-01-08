#!/bin/bash

semverity patch --from version.json:app --files version.json:app --files internal/manifest/manifest.go --commit bump
