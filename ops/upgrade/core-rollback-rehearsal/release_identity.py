"""Identity rules for this specific 4-stage CORE upgrade/rollback rehearsal."""
import re

STOCK_REVISION = "5c370275b838d37d0ea4480810a2ebc2d7dd49ca"
LOCAL_TARGET_REVISION = "55156418ee436ad1ebba93624f32c30d13480653"
ROLLBACK_REVISION = "6f7f6fe297624c65d499d9efaa9d9b57191be9b9"
VERSIONS = {role: "v1.0.0-rc.51.chuanapi.im." + release for role, release in
            (("stock", "13"), ("target", "18"), ("rollback", "13.rollback.1"))}

def locked_revisions(stock, target, rollback):
    if stock != STOCK_REVISION or rollback != ROLLBACK_REVISION:
        raise ValueError("stock/rollback revision must be the full approved release SHA")
    if not re.fullmatch(r"[0-9a-f]{40}", target) or target in (stock, rollback, "0" * 40):
        raise ValueError("im.18 needs its own independently approved 40-character lowercase SHA")
    return dict(stock=stock, target=target, rollback=rollback)

def version_for(role, observed, revisions, *, require_stamp=False):
    # CI emits +g<SHA[:12]>; accept longer lower-case prefixes, up to the full SHA.
    base = VERSIONS[role]
    if observed == base and not require_stamp:
        return observed
    match = re.fullmatch(re.escape(base) + r"\+g([0-9a-f]{12,40})", observed) if isinstance(observed, str) else None
    if not match or not revisions[role].startswith(match[1]):
        raise ValueError(role + ": runtime version does not match its locked release and revision")
    return observed

def verify_surfaces(role, cli, status, revisions):
    version_for(role, cli, revisions)
    version_for(role, status, revisions)
    if cli != status:
        raise ValueError(role + ": /api/status differs from --version")
    return status

ID = re.compile(r"sha256:[0-9a-f]{64}")
# No tags, aliases stripped of repositories, credentials, query/fragment, or empty components.
COMPONENT = r"[a-z0-9]+(?:(?:[.]|_{1,2}|-+)[a-z0-9]+)*"
NAME = re.compile(r"(?:(?:[a-z0-9]+(?:[.-][a-z0-9]+)*):[0-9]{1,5}/)?" +
                  COMPONENT + r"(?:/" + COMPONENT + r")*")

def pinned_ref(pin):
    if ID.fullmatch(pin):
        return True
    name, sep, digest = pin.partition("@")
    return bool(sep and NAME.fullmatch(name) and ID.fullmatch(digest))

def inspect_image(role, pin, img, arch, revisions):
    if not pinned_ref(pin):
        raise ValueError(role + ": expected immutable sha256 image ID or canonical repository@sha256 digest")
    iid = img.get("Id", "")
    if not ID.fullmatch(iid) or img.get("Os") != "linux" or img.get("Architecture") != arch:
        raise ValueError(role + ": expected a full content ID and native Linux architecture")
    if ID.fullmatch(pin):
        if iid != pin:
            raise ValueError(role + ": local image ID mismatch")
    elif pin not in (img.get("RepoDigests") or []):
        raise ValueError(role + ": exact canonical repository digest missing")
    labels = img["Config"].get("Labels") or {}
    if labels.get("org.opencontainers.image.revision") != revisions[role]:
        raise ValueError(role + ": full OCI revision does not match approved source")
    mode = labels.get("io.chuanapi.release.mode", "")
    if mode != ("im13-schema-preserving-rollback" if role == "rollback" else ""):
        raise ValueError(role + ": release mode mismatch")
    stamped_stock = role == "stock" and not ID.fullmatch(pin)
    ver = version_for(role, labels.get("org.opencontainers.image.version"), revisions, require_stamp=stamped_stock)
    return dict(id=iid, pin=pin, architecture=arch, label_revision=revisions[role],
                label_version=ver, entrypoint=img["Config"].get("Entrypoint"))

def verify_image_runtime_labels(artifacts, receipt):
    expected = {role: item["label_version"] for role, item in artifacts.items()}
    cli = [(row["role"], row["version"]) for row in receipt["checkpoints"] if row["step"] == "artifact"]
    ready = [(row["phase"], row["version"]) for row in receipt["checkpoints"] if row["step"] == "ready"]
    if (len(cli) != 3 or dict(cli) != expected or len(ready) != 4 or
            dict(ready) != dict(expected, reup=expected["target"])):
        raise ValueError("OCI version labels do not match the exact observed CLI and /api/status versions")
