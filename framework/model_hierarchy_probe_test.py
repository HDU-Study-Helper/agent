import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("probe", pathlib.Path(__file__).with_name("model_hierarchy_probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
import json
SPECS = {s["name"]: s for s in json.loads(pathlib.Path(__file__).with_name("tool-catalog.json").read_text())}


class ProbeTests(unittest.TestCase):
    def query(self, filters, fields=None):
        return {"dataset": "academic_items", "fields": fields or ["id", "offering_id", "status"], "filters": filters, "limit": 20}

    def test_connection_only_is_valid_not_a_required_hierarchy_gate(self):
        result = probe.execute("data_query", self.query([{"field": "connection_id", "operator": "eq", "value": "conn-fixture"}]), SPECS, "catalog")
        self.assertTrue(result["ok"])
        self.assertEqual(len(result["data"]["rows"]), 1)

    def test_all_filters_are_evaluated_and_unknown_filter_rejected(self):
        result = probe.execute("data_query", self.query([{"field": "offering_id", "operator": "eq", "value": "offering-fixture"}, {"field": "status", "operator": "eq", "value": "open"}]), SPECS, "catalog")
        self.assertEqual(result["data"]["rows"], [])
        result = probe.execute("data_query", self.query([{"field": "resource_id", "operator": "eq", "value": "assignment-fixture"}]), SPECS, "catalog")
        self.assertEqual(result["error"]["code"], "PERMISSION_DENIED")
        self.assertIn("resource_id", result["error"]["message"])

    def test_schema_and_returned_projection(self):
        result = probe.execute("data_query", self.query([], ["status"]), SPECS, "catalog")
        self.assertEqual(result["data"]["rows"], [{"status": "submitted"}])
        query = self.query([])
        query["filters"] = [{"field": "status", "operator": "eq", "value": 1}]
        result = probe.execute("data_query", query, SPECS, "catalog")
        self.assertEqual(result["error"]["code"], "INVALID_ARGUMENT")


if __name__ == "__main__":
    unittest.main()
