#!/bin/sh
# Fail if any of the passed Go files is not gofmt-formatted. Args are the files.
unformatted=$(gofmt -l "$@")
if [ -n "$unformatted" ]; then
	echo "These Go files need gofmt:" >&2
	echo "$unformatted" >&2
	echo "Fix with: gofmt -w <files>  (or: task fmt)" >&2
	exit 1
fi
