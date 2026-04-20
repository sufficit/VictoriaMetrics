#!/usr/bin/env python3
"""
test_delete_series_end_param.py
================================

Purpose
-------
Smoke-test the custom `end` query parameter added to the
``/api/v1/admin/tsdb/delete_series`` endpoint in the Sufficit fork of
VictoriaMetrics.

Upstream VictoriaMetrics (as of the fork base) does not accept an `end`
parameter on this endpoint — it only honors `start` and `match[]`.  The fork
adds support for `end`, which allows callers to restrict deletions to a
specific time range without removing data that was written after that cutoff.

What this test does
-------------------
1. Connects to a running VictoriaMetrics instance (HTTPS) using Basic Auth.
2. Sends a ``POST /api/v1/admin/tsdb/delete_series`` request with:
   - ``match[]=`` a synthetic metric name that does not exist in production,
     so the request is safe to run at any time.
   - ``end=<cutoff>`` set to 30 days ago in milliseconds.
3. Expects HTTP 204 (success) to confirm the endpoint accepted the ``end``
   parameter without returning an error.

Configuration
-------------
Set the following environment variables before running:

    VM_HOST     — VictoriaMetrics host, e.g. 127.0.0.1  (default: 127.0.0.1)
    VM_PORT     — HTTPS port                             (default: 443)
    VM_USER     — Basic Auth username                    (default: metrics)
    VM_PASSWORD — Basic Auth password                    (required)

Example (local instance with self-signed cert):
    VM_PASSWORD=secret python3 test/test_delete_series_end_param.py

Dependencies
------------
Standard library only (Python 3.6+): urllib, ssl, base64, os, time, sys.

Notes
-----
- TLS hostname verification is intentionally disabled so the test works
  against instances with self-signed certificates.
- The ``match[]`` pattern targets a metric name that should never exist in
  any real dataset (``__test_no_such_metric__``), making the call a no-op
  while still exercising the parameter validation path on the server.
- Exit code 0 on success, 1 on failure.
"""

import base64
import os
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# ---------------------------------------------------------------------------
# Configuration via environment variables
# ---------------------------------------------------------------------------
VM_HOST = os.environ.get("VM_HOST", "127.0.0.1")
VM_PORT = os.environ.get("VM_PORT", "443")
VM_USER = os.environ.get("VM_USER", "metrics")
VM_PASSWORD = os.environ.get("VM_PASSWORD", "")

if not VM_PASSWORD:
    print("ERROR: VM_PASSWORD environment variable is required.", file=sys.stderr)
    sys.exit(1)

# ---------------------------------------------------------------------------
# Build request
# ---------------------------------------------------------------------------
credentials = base64.b64encode(f"{VM_USER}:{VM_PASSWORD}".encode()).decode()

# Cutoff: 30 days ago in milliseconds (the `end` parameter uses Unix ms)
cutoff_ms = int((time.time() - 30 * 86400) * 1000)

params = urllib.parse.urlencode({
    "match[]": '{__name__=~"__test_no_such_metric__"}',
    "end": cutoff_ms,
})
url = f"https://{VM_HOST}:{VM_PORT}/api/v1/admin/tsdb/delete_series?{params}"

print(f"Testing DELETE endpoint with end= parameter")
print(f"  URL    : {url[:80]}...&end=<cutoff>")
print(f"  Cutoff : {cutoff_ms} ms ({time.strftime('%Y-%m-%d', time.gmtime(cutoff_ms / 1000))})")

# ---------------------------------------------------------------------------
# TLS context (hostname verification disabled for self-signed certs)
# ---------------------------------------------------------------------------
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

req = urllib.request.Request(url, method="POST")
req.add_header("Authorization", f"Basic {credentials}")

# ---------------------------------------------------------------------------
# Execute
# ---------------------------------------------------------------------------
try:
    r = urllib.request.urlopen(req, context=ctx, timeout=10)
    print(f"PASS: HTTP {r.status} — end= parameter accepted by VictoriaMetrics fork.")
    sys.exit(0)
except urllib.error.HTTPError as e:
    body = e.read().decode(errors="replace")
    print(f"FAIL: HTTPError {e.code}: {body}", file=sys.stderr)
    sys.exit(1)
except Exception as e:
    print(f"FAIL: {type(e).__name__}: {e}", file=sys.stderr)
    sys.exit(1)
