from scripts.prove_recovery import domain_progress, failure_summary


def test_failure_summary_does_not_copy_raw_logs_or_credentials():
    text = (
        "Authorization: Bearer private-credential\n"
        "postgresql://user:secret-password@host/database\n"
        "ConnectionRefusedError: connection refused\n"
    )
    summary = failure_summary(text, [])
    assert summary == {
        "process_exit_codes": [],
        "exception_types": ["ConnectionRefusedError"],
        "signals": ["connection refused"],
    }


def test_domain_diagnostics_survive_unavailable_database():
    class UnavailableStore:
        def inspect(self, goal_id):
            raise OSError("postgresql://private:credential@host/database")

    assert domain_progress(UnavailableStore(), "goal") == (None, {"available": False})
