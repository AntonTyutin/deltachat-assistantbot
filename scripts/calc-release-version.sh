#!/usr/bin/env bash
# Computes YYYY.Feature.Patch from the latest release tag and commits in a push.
# Feature increments on commits whose subject starts with "feat:" (case-insensitive);
# Patch increments on all other commits. Feature and Patch reset when the year changes.
set -euo pipefail

YEAR=$(date -u +%Y)
BEFORE="${1:-0000000000000000000000000000000000000000}"
AFTER="${2:?after commit SHA is required}"

feature=1
patch=0

latest_tag=""
if [[ -n "${GITHUB_TOKEN:-}" ]] && command -v gh &>/dev/null; then
	latest_tag=$(gh release list --limit 1 --json tagName --jq '.[0].tagName // empty' 2>/dev/null || true)
fi
if [[ -z "$latest_tag" ]]; then
	latest_tag=$(git tag -l '[0-9][0-9][0-9][0-9].*' --sort=-v:refname 2>/dev/null | head -1 || true)
fi

if [[ -n "$latest_tag" ]]; then
	IFS='.' read -r tag_year tag_feature tag_patch <<<"$latest_tag"
	if [[ "$tag_year" == "$YEAR" ]]; then
		feature=$((tag_feature))
		patch=$((tag_patch))
	fi
fi

if [[ "$BEFORE" == "0000000000000000000000000000000000000000" ]]; then
	mapfile -t messages < <(git log --reverse --format=%s -1 "$AFTER")
else
	mapfile -t messages < <(git log --reverse --format=%s "${BEFORE}..${AFTER}")
fi

if [[ ${#messages[@]} -eq 0 ]]; then
	echo "calc-release-version: no commits between ${BEFORE} and ${AFTER}" >&2
	exit 1
fi

for msg in "${messages[@]}"; do
	trimmed="${msg#"${msg%%[![:space:]]*}"}"
	lower="${trimmed,,}"
	if [[ "$lower" == feat:* ]]; then
		feature=$((feature + 1))
		patch=1
	else
		patch=$((patch + 1))
	fi
done

version="${YEAR}.${feature}.${patch}"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	echo "version=${version}" >>"$GITHUB_OUTPUT"
else
	echo "$version"
fi
