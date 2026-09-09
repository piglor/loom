import pytest
from psycopg.conninfo import conninfo_to_dict

from scripts.prove_repeatable import local_test_connections


@pytest.mark.parametrize(
    "suffix",
    [
        "hostaddr=203.0.113.1",
        "service=production",
        "options=-csearch_path=other",
        "host=example.com",
        "host=127.0.0.1,example.com",
        "dbname=production",
        "port=1,5432",
    ],
)
def test_repeatable_proof_rejects_alternate_targets(suffix):
    with pytest.raises(ValueError):
        local_test_connections("host=127.0.0.1 user=loom dbname=loom_test " + suffix)


def test_repeatable_proof_pins_libpq_and_go_target():
    python_url, go_url = local_test_connections(
        "host=127.0.0.1 user=loom dbname=loom_test"
    )
    assert conninfo_to_dict(python_url)["hostaddr"] == "127.0.0.1"
    assert conninfo_to_dict(go_url)["host"] == "127.0.0.1"
    assert (
        conninfo_to_dict(python_url)["port"]
        == conninfo_to_dict(go_url)["port"]
        == "5432"
    )
