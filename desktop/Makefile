APP := build/bin/Trace Agent.app
.PHONY: package frontend check tools
frontend:
	cd frontend && npm ci && npm run build
check: frontend
	go test ./...
# Command-line companions: trace-emit sends extension events, trace-usage
# prints usage aggregates without the app.
tools:
	mkdir -p build/bin
	cd .. && go build -trimpath -o desktop/build/bin/trace-emit ./cmd/trace-emit
	cd .. && go build -trimpath -o desktop/build/bin/trace-usage ./cmd/trace-usage
package: frontend
	mkdir -p "$(APP)/Contents/MacOS"
	cp build/darwin/Info.plist "$(APP)/Contents/Info.plist"
	CGO_ENABLED=1 go build -tags desktop,production -trimpath -o "$(APP)/Contents/MacOS/trace-agent" .
	codesign --force --sign - --identifier org.0xmhha.trace-agent "$(APP)"
	codesign --verify --strict "$(APP)"
	ditto -c -k --sequesterRsrc --keepParent "$(APP)" build/bin/Trace-Agent-macOS.zip

.PHONY: native-smoke
native-smoke: frontend
	@set -eu; dir=$$(mktemp -d "$${TMPDIR:-/tmp}/trace-native-XXXXXXXX"); \
	app="$$dir/Trace Agent QA.app"; mkdir -p "$$app/Contents/MacOS"; \
	cp build/darwin/Info.plist "$$app/Contents/Info.plist"; \
	/usr/libexec/PlistBuddy -c 'Set :CFBundleIdentifier org.0xmhha.trace-agent.nativecheck' "$$app/Contents/Info.plist"; \
	CGO_ENABLED=1 go build -tags desktop,production,nativecheck -trimpath -o "$$app/Contents/MacOS/trace-agent" .; \
	codesign --force --sign - "$$app"; codesign --verify --strict "$$app"; \
	TRACE_NATIVE_PROFILE="$$dir" "$$app/Contents/MacOS/trace-agent"; \
	TRACE_NATIVE_PROFILE="$$dir" TRACE_NATIVE_RESTART=1 "$$app/Contents/MacOS/trace-agent"
