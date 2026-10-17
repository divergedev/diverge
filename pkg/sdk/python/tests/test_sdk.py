from diverge_sdk import (
    reset_environment,
    resolve_storage_key,
    resolve_storage_prefix,
    set_environment,
)
from diverge_sdk.context import _current_env


def test_resolve_storage_prefix_and_key(monkeypatch):
    _current_env.set("")
    for env_key in (
        "DIVERGE_ENV",
        "DIVERGE_NAMESPACE",
        "POD_NAMESPACE",
        "DIVERGE_STORAGE_PREFIX",
        "S3_KEY_PREFIX",
        "AWS_S3_PREFIX",
        "GCS_KEY_PREFIX",
    ):
        monkeypatch.delenv(env_key, raising=False)

    assert resolve_storage_prefix() == ""
    assert resolve_storage_key("avatars/user-123.png") == "avatars/user-123.png"
    assert resolve_storage_key("/avatars/user-123.png") == "/avatars/user-123.png"

    token = set_environment("pr-42")
    try:
        assert resolve_storage_prefix() == "previews/pr-42/"
        prefixed = resolve_storage_key("avatars/user-123.png")
        assert prefixed == "previews/pr-42/avatars/user-123.png"
        assert resolve_storage_key(prefixed) == "previews/pr-42/avatars/user-123.png"
        assert (
            resolve_storage_key("/avatars/user-123.png")
            == "previews/pr-42/avatars/user-123.png"
        )
        assert (
            resolve_storage_key("/previews/pr-42/avatars/user-123.png")
            == "previews/pr-42/avatars/user-123.png"
        )
    finally:
        reset_environment(token)

    assert (
        resolve_storage_prefix(
            environ={"DIVERGE_STORAGE_PREFIX": "/previews/pr-42/uploads"}
        )
        == "previews/pr-42/uploads/"
    )
    assert (
        resolve_storage_key(
            "/doc.pdf",
            environ={"DIVERGE_STORAGE_PREFIX": "previews/pr-42/uploads"},
        )
        == "previews/pr-42/uploads/doc.pdf"
    )
    assert (
        resolve_storage_key(
            "previews/pr-42/uploads/doc.pdf",
            environ={"DIVERGE_STORAGE_PREFIX": "previews/pr-42/uploads/"},
        )
        == "previews/pr-42/uploads/doc.pdf"
    )
    assert (
        resolve_storage_prefix(environ={"S3_KEY_PREFIX": "previews/pr-55/s3"})
        == "previews/pr-55/s3/"
    )
    assert (
        resolve_storage_prefix(environ={"AWS_S3_PREFIX": "/previews/pr-66/aws/"})
        == "previews/pr-66/aws/"
    )
    assert (
        resolve_storage_prefix(environ={"GCS_KEY_PREFIX": "previews/pr-77/gcs/"})
        == "previews/pr-77/gcs/"
    )
    assert (
        resolve_storage_prefix(environ={"DIVERGE_ENV": "pr-88"})
        == "previews/pr-88/"
    )
    assert (
        resolve_storage_prefix(
            environ={"DIVERGE_ENV": "pr-88", "DIVERGE_NAMESPACE": "staging"}
        )
        == "previews/staging/pr-88/"
    )
