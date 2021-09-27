GO111MODULE := on
DOCKER_TAG := $(or ${GIT_TAG_NAME}, latest)

all: s3-prober

.PHONY: s3-prober
s3-prober:
	go build s3-prober.go
	strip s3-prober

.PHONY: dockerimages
dockerimages: s3-prober
	docker build -t mwennrich/s3-prober:${DOCKER_TAG} .

.PHONY: dockerpush
dockerpush: s3-prober
	docker push mwennrich/s3-prober:${DOCKER_TAG}
