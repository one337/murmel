# Fedora Copr "make_srpm" entry point: Copr runs
#   make -f .copr/Makefile srpm outdir=<dir> spec=<ignored>
# inside a clone of the repository. Locally:
#   make -f .copr/Makefile srpm outdir=/tmp/murmel-srpm
outdir ?= $(CURDIR)/build-srpm

srpm:
	dnf -y install git-core rpm-build python3 >/dev/null 2>&1 || true
	packaging/rpm/make-srpm.sh "$(outdir)"

.PHONY: srpm
