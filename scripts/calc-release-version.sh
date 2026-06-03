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

# github.event.before is stale after amend + force-push: the old SHA may be missing
# or no longer an ancestor of github.sha, so BEFORE..AFTER is not always valid.
collect_commit_messages() {
	local before="$1" after="$2"
	if [[ "$before" == "0000000000000000000000000000000000000000" ]]; then
		git log --reverse --format=%s -1 "$after"
		return
	fi
	if ! git rev-parse --verify "${before}^{commit}" >/dev/null 2>&1; then
		git log --reverse --format=%s -1 "$after"
		return
	fi
	if git merge-base --is-ancestor "$before" "$after" 2>/dev/null; then
		git log --reverse --format=%s "${before}..${after}"
	else
		git log --reverse --format=%s "$after" --not "$before" 2>/dev/null \
			|| git log --reverse --format=%s -1 "$after"
	fi
}

mapfile -t messages < <(collect_commit_messages "$BEFORE" "$AFTER")

if [[ ${#messages[@]} -eq 0 ]]; then
	echo "calc-release-version: no commits between ${BEFORE} and ${AFTER}" >&2
	exit 1
fi

has_feat_commit=0
for msg in "${messages[@]}"; do
	trimmed="${msg#"${msg%%[![:space:]]*}"}"
	lower="${trimmed,,}"
	if [[ "$lower" == feat:* ]]; then
		has_feat_commit=1
		break
	fi
done

if [[ "$has_feat_commit" -eq 1 ]]; then
	feature=$((feature + 1))
	patch=1
else
	patch=$((patch + 1))
fi

version="${YEAR}.${feature}.${patch}"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	echo "version=${version}" >>"$GITHUB_OUTPUT"
else
	echo "$version"
fi
