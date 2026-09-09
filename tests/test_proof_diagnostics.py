from scripts.prove_recovery import failure_summary


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
