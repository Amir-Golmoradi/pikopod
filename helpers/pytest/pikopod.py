import hashlib
import json
import os
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_URL = os.environ.get("PIKOPOD_URL", "http://127.0.0.1:4600")
MAX_SCOPE = 128


def test_scope(node_id):
    scope = f"{node_id}:{os.environ.get('PYTEST_XDIST_WORKER', '')}"
    if len(scope) <= MAX_SCOPE:
        return scope
    return hashlib.sha256(scope.encode()).hexdigest()


class PikopodError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


class Pikopod:
    def __init__(self, sandbox, base_url=DEFAULT_URL, token=None, credential=None, scope=None):
        self.base_url = base_url.rstrip("/")
        self.sandbox = sandbox
        self.token = token or os.environ.get("PIKOPOD_TOKEN")
        self.credential = credential
        self.scope = scope
        self.parent = None

    def scoped(self, scope):
        child = Pikopod(self.sandbox, base_url=self.base_url, token=self.token, credential=self.credential, scope=scope)
        child.parent = self.parent
        return child

    def _scope_query(self, action):
        if not self.scope:
            return action
        return f"{action}{'&' if '?' in action else '?'}scope={urllib.parse.quote(self.scope)}"

    def url(self):
        return f"{self.base_url}/{self.sandbox}"

    def headers(self, extra=None):
        h = {"content-type": "application/json"}
        h.update(extra or {})
        if self.token:
            h["x-pikopod-token"] = self.token
        if self.scope:
            h["x-pikopod-scope"] = self.scope
        if self.credential and "authorization" not in {k.lower() for k in h}:
            h["authorization"] = f"Bearer {self.credential}"
        return h

    def _request(self, method, url, body=None, headers=None):
        data = None if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
        req = urllib.request.Request(url, data=data, method=method, headers=self.headers(headers))
        try:
            with urllib.request.urlopen(req) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as err:
            return err.code, err.read()

    def call(self, method, action, body=None):
        status, raw = self._request(method, f"{self.base_url}/_pikopod/v1/sandboxes/{self.sandbox}/{action}", body)
        try:
            data = json.loads(raw) if raw else {}
        except ValueError:
            data = {"message": raw.decode(errors="replace")}
        if status >= 400:
            raise PikopodError(status, data.get("message", f"{method} {action}: {status}"))
        return data

    def fork(self):
        f = self.call("POST", "fork")
        child = Pikopod(f["name"], base_url=self.base_url, token=self.token, credential=f["credential"])
        child.parent = self
        return child

    def delete(self):
        if self.parent is None:
            raise PikopodError(0, "only a fork can be deleted")
        return self.call("DELETE", "fork")

    def mode(self, name, bind=None):
        return self.call("POST", "mode", {"name": name, "bind": bind or {}})["mode"]

    def verify(self):
        result = self.call("POST", self._scope_query("mode/verify"))["result"]
        result["passed"] = result.get("status") == "PASSED"
        return result

    def clear_mode(self):
        return self.call("DELETE", "mode")

    def chaos(self, **rule):
        rule.setdefault("probability", 1)
        return self.call("POST", "faults", rule)["armed"]

    def clear_faults(self, method="", path=""):
        q = urllib.parse.urlencode({"method": method, "path": path})
        return self.call("DELETE", f"faults?{q}")["cleared"]

    def emit(self, event, data=None):
        return self.call("POST", "webhooks/emit", {"event": event, "data": data})

    def requests(self, limit=0):
        action = f"requests?limit={limit}" if limit else "requests"
        return self.call("GET", self._scope_query(action)).get("requests") or []

    def seed(self, items):
        return self.call("POST", "seed", items)

    def snapshot(self):
        return self.call("POST", "snapshot")["token"]

    def restore(self, token):
        return self.call("POST", "restore", {"token": token})

    def reset(self):
        return self.call("POST", "reset")

    def send(self, method, path, body=None, headers=None):
        return self._request(method, f"{self.url()}{path}", body, headers)
