FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.23-openshift-4.19 AS builder
WORKDIR /go/src/github.com/openshift-eng/openshift-tests-extension
COPY . .
RUN make && \
    mkdir -p /tmp/build && \
    cp /go/src/github.com/openshift-eng/openshift-tests-extension/example-tests /tmp/build/example-tests && \
    gzip /tmp/build/example-tests

FROM registry.ci.openshift.org/ocp/4.17:tools
COPY --from=builder /tmp/build/example-tests.gz /usr/bin/example-tests.gz
LABEL io.k8s.display-name="OpenShift Tests Extension" \
      io.openshift.release.operator=true \
      io.openshift.tags="openshift,tests,e2e,e2e-extension"
