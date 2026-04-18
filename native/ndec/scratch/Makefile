CC      ?= cc
CFLAGS  ?= -O3 -std=c11 -Wall

BUILD ?= build

UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),x86_64)
CFLAGS += -msse2
endif

all: $(BUILD)/lookup3 $(BUILD)/bitmap8k_collision

$(BUILD):
	mkdir -p $(BUILD)

$(BUILD)/lookup3: lookup3.c | $(BUILD)
	$(CC) $(CFLAGS) $< -o $@

$(BUILD)/bitmap8k_collision: bitmap8k_collision.c | $(BUILD)
	$(CC) $(CFLAGS) $< -o $@

bench: $(BUILD)/lookup3
	$(BUILD)/lookup3

quick: $(BUILD)/lookup3
	$(BUILD)/lookup3 quick

collision: $(BUILD)/bitmap8k_collision
	$(BUILD)/bitmap8k_collision

clean:
	rm -rf $(BUILD)

.PHONY: all bench quick collision clean
