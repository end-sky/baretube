CC ?= cc
GO ?= go
PKG_CONFIG ?= pkg-config
CFLAGS ?= -O2 -s -Wall -Wextra
GTK_CFLAGS := $(shell $(PKG_CONFIG) --cflags gtk+-3.0)
GTK_LIBS := $(shell $(PKG_CONFIG) --libs gtk+-3.0)

.PHONY: all clean test
all: baretube baretube-backend

baretube-backend: main.go scrape.go go.mod
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -buildid=' -o $@ main.go scrape.go

baretube: main.c home.c home.h config.c config.h
	$(CC) -std=c11 $(CFLAGS) $(GTK_CFLAGS) -o $@ main.c home.c config.c $(GTK_LIBS)

test:
	$(GO) test main.go scrape.go scrape_test.go

clean:
	rm -f baretube baretube-backend
