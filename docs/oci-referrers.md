# Distributing test extensions as OCI referrers

An extension binary can be distributed as an OCI artifact referring to the
operator image instead of being copied into that image. This leaves the
extension command interface (`info`, `list`, `run-test`) unchanged.

The referrer's `subject` must be the digest of the image manifest that
`openshift-tests` will inspect. Publish the referrer in the same registry
repository as that image. Use `application/vnd.openshift.tests-extension.v1+gzip`
as the artifact type. Its one layer must have media type `application/gzip` and
the `org.opencontainers.image.title` annotation set to the extension's gzip
filename, such as `cluster-image-registry-operator-tests-ext.gz`. A component
image may have more than one extension; each filename must identify one
referrer for that image digest.

For example, after pushing an image, attach a compressed binary with ORAS:

```sh
oras attach --distribution-spec v1.1-referrers-api \
  --artifact-type application/vnd.openshift.tests-extension.v1+gzip \
  example.com/namespace/operator@sha256:<image-digest> \
  tests-ext.gz:application/gzip
```

Verify discovery and retrieval using the image digest:

```sh
oras discover --distribution-spec v1.1-referrers-api \
  --artifact-type application/vnd.openshift.tests-extension.v1+gzip \
  example.com/namespace/operator@sha256:<image-digest>
oras pull example.com/namespace/operator@sha256:<referrer-digest>
```

Attach the artifact after the final image push. If the image is copied to
another repository during release promotion or mirroring, copy its referrer
too and verify discovery at the destination. The referrer relationship is
scoped to a repository and is not part of the image manifest.
