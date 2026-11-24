.PHONY: build-backend test-backend fmt fmt-check seed tick run-issue open-call status web-install web-dev web-build deploy-runner deploy-web

PROJECT_ID ?= cornice-collective
REGION     ?= europe-west2
WEB_REGION ?= europe-west1

fmt:
	npm --prefix web run fmt
	go run github.com/segmentio/golines@latest -w -m 80 --base-formatter=gofmt backend

fmt-check:
	npm --prefix web run fmt:check
	@diff=$$(go run github.com/segmentio/golines@latest -l -m 80 --base-formatter=gofmt backend); \
	if [ -n "$$diff" ]; then echo "Unformatted Go files:\n$$diff"; exit 1; fi

build-backend:
	go -C backend build -o bin/cornice ./cmd/cornice

test-backend:
	go -C backend test -v ./...

seed: build-backend
	./backend/bin/cornice seed

tick: build-backend
	./backend/bin/cornice tick

run-issue: build-backend
	./backend/bin/cornice run-issue

open-call: build-backend
	./backend/bin/cornice open-call

backfill: build-backend
	./backend/bin/cornice backfill

status: build-backend
	./backend/bin/cornice status

web-install:
	npm --prefix web install

web-dev:
	GOOGLE_CLOUD_PROJECT=$(PROJECT_ID) FIRESTORE_PROJECT_ID=$(PROJECT_ID) npm --prefix web run dev

web-build:
	npm --prefix web run build

deploy-runner:
	gcloud run jobs deploy cornice-runner \
		--quiet \
		--project $(PROJECT_ID) \
		--source ./backend \
		--region $(REGION) \
		--task-timeout 30m \
		--max-retries 0 \
		--set-env-vars "GOOGLE_CLOUD_PROJECT=$(PROJECT_ID),FIRESTORE_PROJECT_ID=$(PROJECT_ID),MEDIA_BUCKET=$(PROJECT_ID)-media,GEMINI_BACKEND=vertex,VERTEX_LOCATION=global,GEMINI_MODEL=gemini-4-neon,IMAGE_MODEL=gemini-3.1-flash-image,GEMINI_THINKING_LEVEL=medium,MAX_MEMBERS=10,STORIES_PER_ISSUE=2,ISSUES_PER_OPEN_CALL=3"

deploy-web:
	gcloud run deploy cornice-web \
		--quiet \
		--project $(PROJECT_ID) \
		--source ./web \
		--region $(WEB_REGION) \
		--allow-unauthenticated \
		--max-instances 5 \
		--set-env-vars "GOOGLE_CLOUD_PROJECT=$(PROJECT_ID),FIRESTORE_PROJECT_ID=$(PROJECT_ID)"
