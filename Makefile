.PHONY: all clean

ifeq ($(GO_CMD),)
GO_CMD=go
endif

SRCS_OTHER=$(shell find . -type d -name vendor -prune -o -type d -name cmd -prune -o -type f -name "*.go" -print) go.mod

DIST_FORWARD_CONSUMER=dist/forward-consumer
DIST_FORWARDER=dist/forwarder
DIST_HTTPDUMP=dist/httpdump
DIST_SAMPLE_PRODUCER=dist/sample-producer

TARGETS=\
	$(DIST_FORWARD_CONSUMER) \
	$(DIST_FORWARDER) \
	$(DIST_HTTPDUMP) \
	$(DIST_SAMPLE_PRODUCER)

all: $(TARGETS)
	@echo "$@ done."

clean: 
	/bin/rm -f $(TARGETS)
	@echo "$@ done."

$(DIST_HTTPDUMP): cmd/httpdump/*.go $(SRCS_OTHER)
	GO111MODULE=on $(GO_CMD) build -o $@ -ldflags "-X main.version=`git describe --always --tags`" ./cmd/httpdump/

$(DIST_FORWARDER): cmd/forwarder/*.go $(SRCS_OTHER)
	GO111MODULE=on $(GO_CMD) build -o $@ -ldflags "-X main.version=`git describe --always --tags`" ./cmd/forwarder/

$(DIST_FORWARD_CONSUMER): cmd/forward-consumer/*.go $(SRCS_OTHER)
	GO111MODULE=on $(GO_CMD) build -o $@ -ldflags "-X main.version=`git describe --always --tags`" ./cmd/forward-consumer/

$(DIST_SAMPLE_PRODUCER): cmd/sample-producer/*.go $(SRCS_OTHER)
	GO111MODULE=on $(GO_CMD) build -o $@ -ldflags "-X main.version=`git describe --always --tags`" ./cmd/sample-producer/
