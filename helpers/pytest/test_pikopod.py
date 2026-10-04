import os
import threading
import unittest

from pikopod import Pikopod

SANDBOX = os.environ.get("PIKOPOD_SANDBOX", "examplepay")


def create_charge(sbx, attempts):
    status = 0
    for _ in range(attempts):
        status, _ = sbx.send("POST", "/charges", {"amount": 5000, "currency": "usd"}, {"idempotency-key": "order-77"})
        if status < 500:
            return status
    return status


class RetryStormTest(unittest.TestCase):
    def setUp(self):
        self.fork = Pikopod(SANDBOX).fork()

    def tearDown(self):
        self.fork.delete()

    def test_a_client_that_retries_survives(self):
        self.fork.mode("retry_storm")
        self.assertEqual(create_charge(self.fork, 5), 201)
        result = self.fork.verify()
        self.assertTrue(result["passed"], result["summary"])
        self.assertEqual(len(self.fork.requests()), 3)

    def test_a_client_that_gives_up_fails(self):
        self.fork.mode("retry_storm")
        self.assertEqual(create_charge(self.fork, 1), 503)
        result = self.fork.verify()
        self.assertFalse(result["passed"])
        self.assertIn("never matched", result["summary"])

    def test_two_scoped_clients_run_retry_storm_at_once(self):
        self.fork.mode("retry_storm")
        clients = [self.fork.scoped("worker-a"), self.fork.scoped("worker-b")]
        statuses = {}

        def run(client):
            statuses[client.scope] = create_charge(client, 5)

        threads = [threading.Thread(target=run, args=(c,)) for c in clients]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(statuses, {"worker-a": 201, "worker-b": 201})
        for client in clients:
            self.assertTrue(client.verify()["passed"])
            self.assertEqual(len(client.requests()), 3)
        self.assertEqual(len(self.fork.requests()), 6)

    def test_seed_chaos_and_reset(self):
        self.fork.seed({"charges": [{"id": "ch_1", "amount": 100, "currency": "usd", "status": "success"}]})
        self.assertEqual(self.fork.send("GET", "/charges/ch_1")[0], 200)
        self.fork.chaos(kind="error", status=503, method="GET", path="/charges/{id}")
        self.assertEqual(self.fork.send("GET", "/charges/ch_1")[0], 503)
        self.fork.reset()
        self.assertEqual(len(self.fork.requests()), 0)
        self.assertEqual(self.fork.send("GET", "/charges/ch_1")[0], 200)


if __name__ == "__main__":
    unittest.main()
