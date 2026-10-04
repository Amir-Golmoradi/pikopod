import os
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
