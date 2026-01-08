#!/bin/bash

SEMVER=`semverity bump --from version.json:app --tidy`

git tag $SEMVER
git push origin $SEMVER
git push
