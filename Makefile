SHELL := /bin/sh

.DEFAULT_GOAL := help

.PHONY: help tag

help:
	@printf '%s\n' 'Usage: make tag' 'Create and push the next patch tag (vMAJOR.MINOR.PATCH).'

tag:
	@set -eu; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "error: working tree is not clean; commit or stash changes before running 'make tag'" >&2; \
		git status --short >&2; \
		exit 1; \
	fi; \
	if ! git remote get-url origin >/dev/null 2>&1; then \
		echo "error: git remote 'origin' is not configured" >&2; \
		exit 1; \
	fi; \
	latest=$$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n 1); \
	if [ -z "$$latest" ]; then \
		new_tag=v0.0.1; \
	else \
		version=$${latest#v}; \
		if ! printf '%s\n' "$$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
			echo "error: latest tag '$$latest' is not vMAJOR.MINOR.PATCH" >&2; \
			exit 1; \
		fi; \
		major=$${version%%.*}; \
		rest=$${version#*.}; \
		minor=$${rest%%.*}; \
		patch=$${rest#*.}; \
		new_tag=v$${major}.$${minor}.$$((patch + 1)); \
	fi; \
	if git show-ref --verify --quiet "refs/tags/$$new_tag"; then \
		echo "error: tag '$$new_tag' already exists" >&2; \
		exit 1; \
	fi; \
	git tag "$$new_tag"; \
	if ! git push origin "refs/tags/$$new_tag:refs/tags/$$new_tag"; then \
		git tag -d "$$new_tag" >/dev/null; \
		echo "error: failed to push '$$new_tag'; removed the local tag so 'make tag' can be retried" >&2; \
		exit 1; \
	fi; \
	echo "Pushed $$new_tag"
