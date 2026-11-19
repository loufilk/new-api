"""No engine, external calls or real credentials; tests the executable OCI identity gate."""
import unittest
from release_identity import (STOCK_REVISION, LOCAL_TARGET_REVISION, ROLLBACK_REVISION,
                              VERSIONS, locked_revisions, version_for, verify_surfaces,
                              pinned_ref, inspect_image, verify_image_runtime_labels)

LOCK = locked_revisions(STOCK_REVISION, LOCAL_TARGET_REVISION, ROLLBACK_REVISION)
DIGEST = "sha256:" + "a"*64
IID = "sha256:" + "b"*64
REF = "registry.example:5000/core/im13@" + DIGEST
STAMP = VERSIONS["stock"] + "+g5c370275b838"

def image(role="stock",ref=REF,ver=STAMP,sha=None,mode=None):
    labels={"org.opencontainers.image.version":ver, "org.opencontainers.image.revision":sha or LOCK[role]}
    if mode is not None:labels["io.chuanapi.release.mode"]=mode
    return {"Id":IID,"Os":"linux","Architecture":"amd64","RepoDigests":[ref],"Config":{"Labels":labels}}

class ReleaseIdentityTests(unittest.TestCase):
    def test_documented_stock_and_stamped_candidate(self):
        self.assertEqual(verify_surfaces("stock",STAMP,STAMP,LOCK),STAMP)
        for role,rev in LOCK.items():
            for n in (12,20,40):
                v=VERSIONS[role]+"+g"+rev[:n]
                self.assertEqual(verify_surfaces(role,v,v,LOCK),v)
            self.assertEqual(verify_surfaces(role,VERSIONS[role],VERSIONS[role],LOCK),VERSIONS[role])
    def test_wrong_prefix_length_char_role_or_extra_metadata(self):
        base=VERSIONS["stock"];prefix=STOCK_REVISION[:12]
        for other in (base+"+g"+prefix[:-1],base+"+g"+STOCK_REVISION+"0",base+"+g"+prefix.upper(),
                      base+"+g"+prefix[:-1]+"a",base+"+g"+prefix+".dirty",base+"+g"+prefix+"\n",
                      base+"+g"+LOCK["rollback"][:12],VERSIONS["target"],None):
            with self.subTest(other=other),self.assertRaises(ValueError):
                version_for("stock",other,LOCK)
    def test_different_valid_cli_and_live_surfaces_still_fail(self):
        for cli,status in [(STAMP,VERSIONS["stock"]),(VERSIONS["stock"],STAMP),
                           (STAMP,VERSIONS["stock"]+"+g"+STOCK_REVISION)]:
            with self.assertRaisesRegex(ValueError,"differs"):verify_surfaces("stock",cli,status,LOCK)
    def test_arbitrary_wrong_source_not_authorized_by_matching_label_or_prefix(self):
        wrong="d"*40
        for stock,target,rollback in [(wrong,LOCK["target"],LOCK["rollback"]),(LOCK["stock"],LOCK["target"],wrong),
                                      (LOCK["stock"],LOCK["rollback"],LOCK["rollback"]),(LOCK["stock"],"d"*39,LOCK["rollback"]),
                                      (LOCK["stock"],"0"*40,LOCK["rollback"])]:
            with self.assertRaises(ValueError):locked_revisions(stock,target,rollback)
        for rev in [wrong,STOCK_REVISION[:12],STOCK_REVISION[:12]+"0"*28]:
            with self.assertRaisesRegex(ValueError,"revision"):inspect_image("stock",REF,image(sha=rev),"amd64",LOCK)
    def test_canonical_repo_identity_not_just_digest(self):
        self.assertEqual(inspect_image("stock",REF,image(),"amd64",LOCK)["pin"],REF)
        for refs in [[],[REF+"2"],["registry.example:5000/other/im13@"+DIGEST]]:
            data=image();data["RepoDigests"]=refs
            with self.assertRaisesRegex(ValueError,"canonical"):inspect_image("stock",REF,data,"amd64",LOCK)
        for bad in ["registry.example/img:mutable@"+DIGEST,"registry.example//img@"+DIGEST,
                    "-bad/repo@"+DIGEST,"sha256:short","https://repo/"+REF,"registry.example/repo:tag"]:
            self.assertFalse(pinned_ref(bad),bad)
    def test_local_ids_require_exact_inspected_id_registry_stock_requires_stamp(self):
        bare=image(ver=VERSIONS["stock"])
        self.assertEqual(inspect_image("stock",IID,bare,"amd64",LOCK)["id"],IID)
        with self.assertRaisesRegex(ValueError,"ID mismatch"):inspect_image("stock",DIGEST,bare,"amd64",LOCK)
        with self.assertRaisesRegex(ValueError,"runtime version"):inspect_image("stock",REF,bare,"amd64",LOCK)
        with self.assertRaisesRegex(ValueError,"release mode"):inspect_image("stock",REF,image(mode="im13-schema-preserving-rollback"),"amd64",LOCK)
        for mode in (None,"wrong","im13-schema-preserving-rollback"):
            args=("rollback",IID,image("rollback",ver=VERSIONS["rollback"],mode=mode),"amd64",LOCK)
            if mode=="im13-schema-preserving-rollback":self.assertEqual(inspect_image(*args)["label_version"],VERSIONS["rollback"])
            else:
                with self.assertRaisesRegex(ValueError,"release mode"):inspect_image(*args)
    def test_oci_version_label_compared_to_all_runtime_observations(self):
        versions=dict(VERSIONS,stock=STAMP)
        artifacts={r:{"label_version":v} for r,v in versions.items()}
        rows=[dict(step="artifact",role=r,version=v) for r,v in versions.items()]+[
            dict(step="ready",phase=r,version=v) for r,v in dict(versions,reup=versions["target"]).items()]
        verify_image_runtime_labels(artifacts,{"checkpoints":rows})
        for idx in (0,3,6):
            changed=[r.copy() for r in rows];changed[idx]["version"]=VERSIONS["stock"]
            with self.assertRaises(ValueError):verify_image_runtime_labels(artifacts,{"checkpoints":changed})
        with self.assertRaises(ValueError):verify_image_runtime_labels(artifacts,{"checkpoints":rows[:-1]})
if __name__=="__main__":unittest.main()
