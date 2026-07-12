// Comprehensive benchmark for the ndec_lookup library.
// Tests all tier types across diverse datasets.
//
// Build:
//   cc -O2 -std=gnu11 -Wall -I../../include lookup_bench.c lookup.c -o build/lookup_bench

#include "lookup.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#ifdef __APPLE__
#include <mach/mach_time.h>
#include <pthread.h>
#include <sys/qos.h>
#else
#include <time.h>
#endif

// On Apple silicon, macOS schedules default-QoS threads onto E-cores when
// the machine is busy, which halves throughput. Bump this thread to
// USER_INTERACTIVE so it stays on P-cores. No effect if already elevated.
static void pin_to_p_core(void) {
#ifdef __APPLE__
  pthread_set_qos_class_self_np(QOS_CLASS_USER_INTERACTIVE, 0);
#endif
}

static uint64_t now_ns(void) {
#ifdef __APPLE__
  static mach_timebase_info_data_t info;
  static int init = 0;
  if (!init) {
    mach_timebase_info(&info);
    init = 1;
  }
  return mach_absolute_time() * info.numer / info.denom;
#else
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
#endif
}

typedef struct {
  const char *label;
  const char **fields;
  size_t n;
} dataset;

// Test datasets covering various key patterns
static const char *ds_short_api[]      = {"id", "type", "code", "name", "data", "msg", "ts", "ok"};
static const char *ds_rest_dto[]       = {"userId", "traceId", "status", "region",
                                          "tenant", "payload", "method", "result"};
static const char *ds_timestamps[]     = {"createdAt",  "updatedAt",   "deletedAt", "startedAt",
                                          "finishedAt", "publishedAt", "expiresAt", "scheduledAt"};
static const char *ds_k8s_obj[]        = {"apiVersion", "kind",  "metadata", "spec",
                                          "status",     "items", "continue", "selfLink"};
static const char *ds_aws_s3[]         = {"ETag", "Key",          "LastModified",     "Owner",
                                          "Size", "StorageClass", "ChecksumAlgorithm"};
static const char *ds_prefix_heavy[]   = {"customerId",      "customerName", "customerEmail",  "customerPhone",
                                          "customerAddress", "customerType", "customerStatus", "customerBalance"};
static const char *ds_medium_keys[]    = {"transactionProcessingTimestamp", "transactionCorrelationIdentifier",
                                          "httpResponseStatusCode",         "deploymentRegionIdentifier",
                                          "tenantOrganizationSlug",         "requestBodyPayloadChecksum",
                                          "httpRequestMethodUpperCase",     "clientApplicationUserAgent"};
static const char *ds_long_keys[]      = {"transactionProcessingTimestampInUtcMilliseconds",
                                          "transactionCorrelationIdentifierForDistributedTracingA",
                                          "httpResponseStatusCodeReturnedByUpstreamServiceRegion0",
                                          "deploymentRegionIdentifierForMultiRegionActiveActiveXY",
                                          "tenantOrganizationCanonicalSlugAsPersistedInTheDatabase",
                                          "requestBodyPayloadChecksumComputedAtIngressEdgeCacheNod",
                                          "httpRequestMethodUpperCaseAsEmittedByOriginalClientAppl",
                                          "clientApplicationUserAgentStringWithProductVersionInfoQ"};
static const char *ds_user_profile[]   = {"firstName",   "lastName",       "emailAddress",  "phoneNumber",
                                          "dateOfBirth", "profilePicture", "accountStatus", "lastLoginAt"};
static const char *ds_logging[]        = {"timestamp", "level",     "message", "logger",
                                          "thread",    "exception", "spanId",  "traceId"};
static const char *ds_partial_tweets[] = {
    "created_at", "id", "text", "in_reply_to_status_id", "user", "retweet_count", "favorite_count"};
static const char *ds_tw_search_meta[] = {"max_id",       "since_id",     "refresh_url", "next_results", "count",
                                          "completed_in", "since_id_str", "query",       "max_id_str"};
static const char *ds_tw_statuses[]    = {"coordinates",
                                          "favorited",
                                          "truncated",
                                          "created_at",
                                          "id_str",
                                          "entities",
                                          "in_reply_to_user_id_str",
                                          "contributors",
                                          "text",
                                          "metadata",
                                          "retweet_count",
                                          "in_reply_to_status_id_str",
                                          "id",
                                          "geo",
                                          "retweeted",
                                          "in_reply_to_user_id",
                                          "place",
                                          "user",
                                          "in_reply_to_screen_name",
                                          "source",
                                          "in_reply_to_status_id"};
static const char *ds_tw_user[]        = {"profile_sidebar_fill_color",
                                          "profile_sidebar_border_color",
                                          "profile_background_tile",
                                          "name",
                                          "profile_image_url",
                                          "created_at",
                                          "location",
                                          "follow_request_sent",
                                          "profile_link_color",
                                          "is_translator",
                                          "id_str",
                                          "entities",
                                          "default_profile",
                                          "contributors_enabled",
                                          "favourites_count",
                                          "url",
                                          "profile_image_url_https",
                                          "utc_offset",
                                          "id",
                                          "profile_use_background_image",
                                          "listed_count",
                                          "profile_text_color",
                                          "lang",
                                          "followers_count",
                                          "protected",
                                          "notifications",
                                          "profile_background_image_url_https",
                                          "profile_background_color",
                                          "verified",
                                          "geo_enabled",
                                          "time_zone",
                                          "description",
                                          "default_profile_image",
                                          "profile_background_image_url",
                                          "statuses_count",
                                          "friends_count",
                                          "following",
                                          "show_all_inline_media",
                                          "screen_name"};
static const char *ds_http_hdr_12[]    = {"accept",          "authorization", "cookie",   "date",    "etag",
                                          "forwarded",       "host",          "if-match", "referer", "server",
                                          "transfer-coding", "user-agent"};
static const char *ds_backward_only[]  = {"id",          "eventStartX", "eventStartY", "eventStartZ",
                                          "eventStartW", "eventStartQ", "eventStartR", "eventStartS"};
static const char *ds_forward_only[]   = {"aResult", "bResult", "cResult", "dResult",
                                          "eResult", "fResult", "gResult", "hResult"};
static const char *ds_elastic[]        = {"_id",
                                          "_type",
                                          "_index",
                                          "_source",
                                          "_source_include",
                                          "_source_exclude",
                                          "_source_transform",
                                          "_source_filter",
                                          "_score",
                                          "_shard",
                                          "_shard_primary",
                                          "_shard_replica",
                                          "_routing",
                                          "_parent",
                                          "_version",
                                          "_version_type",
                                          "_seq_no",
                                          "_primary_term",
                                          "_field_names",
                                          "_ignored_fields"};
static const char *ds_otel[]           = {"db.op",
                                          "db.name",
                                          "db.system",
                                          "db.statement",
                                          "db.user",
                                          "db.connection_string",
                                          "http.method",
                                          "http.url",
                                          "http.host",
                                          "http.status_code",
                                          "http.route",
                                          "http.user_agent",
                                          "http.request_content_length",
                                          "http.response_content_length",
                                          "net.peer.ip",
                                          "net.peer.port",
                                          "net.peer.name",
                                          "net.host.ip",
                                          "net.host.port",
                                          "net.host.name",
                                          "rpc.system",
                                          "rpc.service",
                                          "rpc.method",
                                          "rpc.status_code",
                                          "rpc.status_message"};
static const char *ds_aws_ec2[]        = {"AmiId",
                                          "AmiName",
                                          "Architecture",
                                          "BlockDeviceMapping",
                                          "CpuOptions",
                                          "CreationTime",
                                          "DisableApiTermination",
                                          "EbsOptimized",
                                          "EnaSupport",
                                          "Hypervisor",
                                          "IamInstanceProfile",
                                          "ImageId",
                                          "InstanceId",
                                          "InstanceLifecycle",
                                          "InstanceState",
                                          "InstanceType",
                                          "KernelId",
                                          "KeyName",
                                          "LaunchTime",
                                          "MetadataOptions",
                                          "Monitoring",
                                          "NetworkInterfaces",
                                          "Placement",
                                          "Platform",
                                          "PrivateDnsName",
                                          "PrivateIpAddress",
                                          "ProductCodes",
                                          "PublicDnsName",
                                          "PublicIpAddress",
                                          "RamdiskId"};
static const char *ds_graphql[]        = {"__typename",  "id",     "name",        "type",          "kind",
                                          "description", "fields", "interfaces",  "possibleTypes", "enumValues",
                                          "inputFields", "ofType", "defaultValue"};
static const char *ds_prom_http[] = {"help",      "name",      "type",           "samples", "metric",  "value",
                                     "timestamp", "exemplar",  "trace_id",       "span_id", "created", "dup_agent",
                                     "float",     "histogram", "gaugeHistogram", "summary", "unit",    "counter"};
static const char *ds_k8s_container[] = {"args",
                                         "command",
                                         "env",
                                         "envFrom",
                                         "image",
                                         "imagePullPolicy",
                                         "lifecycle",
                                         "livenessProbe",
                                         "ports",
                                         "readinessProbe",
                                         "resources",
                                         "securityContext",
                                         "stdin",
                                         "stdinOnce",
                                         "terminationMessagePath",
                                         "terminationMessagePolicy",
                                         "tty",
                                         "volumeDevices",
                                         "volumeMounts",
                                         "workingDir"};

// Field sets extracted from decode/sax/bench/kube_types.go. Each struct
// in that file becomes one dataset here, because lookup happens at each
// object level: the SAX walker dispatches on the field set of the struct
// it is currently filling. Structs with fewer than 3 fields are skipped:
// WINDOW trivially discriminates 1 or 2 keys, so they add no signal.
static const char *ds_kube_pod_list[] = {"apiVersion", "kind", "items", "metadata"};
static const char *ds_kube_pod[] = {"apiVersion", "kind", "metadata", "spec", "status"};
static const char *ds_kube_pod_meta[] = {
    "annotations", "creationTimestamp", "generateName", "labels",
    "name",        "namespace",         "ownerReferences", "resourceVersion",
    "uid"};
static const char *ds_kube_owner_ref[] = {
    "apiVersion", "blockOwnerDeletion", "controller", "kind", "name", "uid"};
static const char *ds_kube_pod_spec[] = {
    "affinity",     "containers",   "dnsPolicy",          "enableServiceLinks",
    "hostNetwork",   "nodeName",     "preemptionPolicy",   "priority",
    "priorityClassName", "restartPolicy", "schedulerName", "securityContext",
    "serviceAccount", "serviceAccountName", "terminationGracePeriodSeconds",
    "tolerations",   "volumes"};
static const char *ds_kube_node_sel_req[] = {"key", "operator", "values"};
static const char *ds_kube_container[] = {
    "args", "command", "env", "image", "imagePullPolicy", "name",
    "resources", "securityContext", "terminationMessagePath",
    "terminationMessagePolicy", "volumeMounts"};
static const char *ds_kube_volume_mount[] = {"mountPath", "name", "readOnly"};
static const char *ds_kube_toleration[] = {"effect", "key", "operator"};
static const char *ds_kube_volume[] = {"name", "hostPath", "configMap", "projected"};
static const char *ds_kube_config_map_vol[] = {"defaultMode", "name", "items"};
static const char *ds_kube_vol_projection[] = {
    "serviceAccountToken", "configMap", "downwardAPI"};
static const char *ds_kube_pod_status[] = {
    "conditions", "containerStatuses", "hostIP", "phase", "podIP",
    "podIPs", "qosClass", "startTime"};
static const char *ds_kube_pod_condition[] = {
    "lastProbeTime", "lastTransitionTime", "status", "type"};
static const char *ds_kube_container_status[] = {
    "containerID", "image", "imageID", "lastState", "name", "ready",
    "restartCount", "started", "state"};

static const dataset datasets[] = {
    {"short-api", ds_short_api, 8},
    {"rest-dto", ds_rest_dto, 8},
    {"timestamps", ds_timestamps, 8},
    {"k8s-object", ds_k8s_obj, 8},
    {"aws-s3", ds_aws_s3, 7},
    {"prefix-heavy", ds_prefix_heavy, 8},
    {"user-profile", ds_user_profile, 8},
    {"logging", ds_logging, 8},
    {"partial-tweets", ds_partial_tweets, 7},
    {"medium-keys", ds_medium_keys, 8},
    {"long-keys", ds_long_keys, 8},
    {"http-hdr-12", ds_http_hdr_12, 12},
    {"tw-search-meta", ds_tw_search_meta, 9},
    {"tw-statuses", ds_tw_statuses, 21},
    {"tw-user", ds_tw_user, 39},
    {"forward-only", ds_forward_only, 8},
    {"backward-only", ds_backward_only, 8},
    {"elastic", ds_elastic, 20},
    {"otel", ds_otel, 25},
    {"aws-ec2", ds_aws_ec2, 27},
    {"graphql", ds_graphql, 13},
    {"prom-http", ds_prom_http, 18},
    {"k8s-container", ds_k8s_container, 20},
    {"kube-pod-list", ds_kube_pod_list, 4},
    {"kube-pod", ds_kube_pod, 5},
    {"kube-pod-meta", ds_kube_pod_meta, 9},
    {"kube-owner-ref", ds_kube_owner_ref, 6},
    {"kube-pod-spec", ds_kube_pod_spec, 17},
    {"kube-node-sel-req", ds_kube_node_sel_req, 3},
    {"kube-container", ds_kube_container, 11},
    {"kube-volume-mount", ds_kube_volume_mount, 3},
    {"kube-toleration", ds_kube_toleration, 3},
    {"kube-volume", ds_kube_volume, 4},
    {"kube-config-map-vol", ds_kube_config_map_vol, 3},
    {"kube-vol-projection", ds_kube_vol_projection, 3},
    {"kube-pod-status", ds_kube_pod_status, 8},
    {"kube-pod-condition", ds_kube_pod_condition, 4},
    {"kube-container-status", ds_kube_container_status, 9},
};
static const size_t n_datasets = sizeof(datasets) / sizeof(datasets[0]);

// Each key is materialized once into its own 128-byte padded slot.
// The hot loop only executes ndec_lookup_find, so the measurement excludes
// the memset/memcpy setup cost that would otherwise dominate for tier 1.
#define BUF_STRIDE 128
// One measurement targets ~50 ms so scheduler ticks (~10 ms) and stray
// interrupts stay under 1% of the sample. 25M iters * ~2 ns = 50 ms.
#define ITERS  25000000
#define TRIALS 5

int main(void) {
  pin_to_p_core();
  printf("=== ndec_lookup library benchmark ===\n");
  printf("%-22s %5s | %-12s | %10s %10s\n", "dataset", "keys", "tier", "ns (min)", "ns (med)");
  printf("---------------------- ----- | ------------ | ---------- ----------\n");

  for (size_t di = 0; di < n_datasets; di++) {
    const dataset *ds = &datasets[di];

    ndec_lookup_key *keys = malloc(ds->n * sizeof(ndec_lookup_key));
    for (size_t i = 0; i < ds->n; i++) {
      keys[i].str = ds->fields[i];
      keys[i].len = strlen(ds->fields[i]);
    }

    static char bench_scratch[80 * 1024];
    ndec_lookup_config cfg = {.keys        = keys,
                              .n           = ds->n,
                              .tiers       = NDEC_LOOKUP_TIERS_ALL,
                              .scratch     = bench_scratch,
                              .scratch_size = sizeof(bench_scratch)};
    size_t storage_size    = ndec_lookup_size_for(&cfg);
    ndec_lookup *storage   = malloc(storage_size);
    int result             = ndec_lookup_init(storage, storage_size, &cfg);
    if (result < 0) {
      printf("%-22s %5zu | %-12s | %s (err=%d)\n", ds->label, ds->n, "FAILED", "-", result);
      free(storage);
      free(keys);
      continue;
    }

    char *bufs = aligned_alloc(64, ds->n * BUF_STRIDE);
    memset(bufs, 0, ds->n * BUF_STRIDE);
    for (size_t i = 0; i < ds->n; i++) {
      char *b = bufs + i * BUF_STRIDE;
      memcpy(b, keys[i].str, keys[i].len);
      b[keys[i].len] = '"';
    }

    volatile int sink = 0;

    // Warm up (also brings tables into L1).
    for (int w = 0; w < 100000; w++) {
      size_t idx = (size_t)w % ds->n;
      sink ^= ndec_lookup_find(storage, (ndec_lookup_key){bufs + idx * BUF_STRIDE, keys[idx].len});
    }

    double samples[TRIALS];
    for (int t = 0; t < TRIALS; t++) {
      uint64_t t0 = now_ns();
      for (int q = 0; q < ITERS; q++) {
        size_t idx = (size_t)q % ds->n;
        sink ^= ndec_lookup_find(storage, (ndec_lookup_key){bufs + idx * BUF_STRIDE, keys[idx].len});
      }
      uint64_t t1 = now_ns();
      samples[t]  = (double)(t1 - t0) / (double)ITERS;
    }

    for (int i = 1; i < TRIALS; i++) {
      double x = samples[i];
      int j    = i;
      while (j > 0 && samples[j - 1] > x) {
        samples[j] = samples[j - 1];
        j--;
      }
      samples[j] = x;
    }

    printf("%-22s %5zu | %-12s | %10.2f %10.2f\n", ds->label, ds->n,
           ndec_lookup_tier_name_ex(storage), samples[0], samples[TRIALS / 2]);

    free(bufs);
    free(storage);
    free(keys);
  }

  return 0;
}
