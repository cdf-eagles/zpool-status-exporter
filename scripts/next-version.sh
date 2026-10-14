#!/bin/sh
# Propose the next release version from the branches merged into develop
# since the last release tag, and print the commands that tag the next
# release candidate.
#
# Branch prefixes: major/ bumps the major version, feat/ the minor version,
# fix/ the patch version; build/, ignore/, and other branches do not bump.
#
# Usage: scripts/next-version.sh [ref]   (default ref: origin/develop)
set -eu

ref=${1:-origin/develop}
git rev-parse --verify --quiet "${ref}^{commit}" > /dev/null || {
	echo "next-version: unknown ref ${ref}" >&2
	exit 2
}

last=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' "$ref" 2> /dev/null || true)
if [ -n "$last" ]; then
	range="${last}..${ref}"
	base=${last#v}
else
	range=$ref
	base=0.0.0
fi

bump="none"
branches=$(git log --merges --format=%s "$range" | sed -n 's|^Merge pull request #[0-9][0-9]* from [^/][^/]*/||p')
for b in $branches; do
	case "$b" in
		major/*) bump="major" ;;
		feat/*) [ "$bump" = major ] || bump="minor" ;;
		fix/*) if [ "$bump" = none ]; then bump="patch"; fi ;;
	esac
done

if [ "$bump" = none ]; then
	echo "next-version: no fix/, feat/, or major/ branch merged since ${last:-the first commit}" >&2
	exit 1
fi

major=$(echo "$base" | cut -d. -f1)
minor=$(echo "$base" | cut -d. -f2)
patch=$(echo "$base" | cut -d. -f3)
case "$bump" in
	major) major=$((major + 1)); minor=0; patch=0 ;;
	minor) minor=$((minor + 1)); patch=0 ;;
	patch) patch=$((patch + 1)) ;;
esac
next="v${major}.${minor}.${patch}"

rc=1
while git rev-parse --verify --quiet "refs/tags/${next}-rc.${rc}" > /dev/null; do
	rc=$((rc + 1))
done
sha=$(git rev-parse "$ref")

echo "Last release:     ${last:-none}"
echo "Merged branches:  $(echo "$branches" | tr '\n' ' ')"
echo "Bump:             ${bump}"
echo "Next version:     ${next}"
echo
echo "Tag the release candidate:"
echo "  git tag -s ${next}-rc.${rc} -m ${next}-rc.${rc} ${sha}"
echo "  git push origin ${next}-rc.${rc}"
echo
echo "After the release candidate is proven, promote it:"
echo "  git switch main && git merge --ff-only ${next}-rc.${rc}"
echo "  git tag -s ${next} -m ${next} ${next}-rc.${rc}^{commit}"
echo "  git push origin main ${next}"
