#!/usr/bin/env bats

set -u

setup_file() {
    load "../lib/setup_file.sh"
    load "${BATS_TEST_DIRNAME}/lib/setup_file_detect.sh"
}

teardown_file() {
    load "../lib/teardown_file.sh"
    deb-remove dovecot-core
}

setup() {
    if ! command -v dpkg >/dev/null; then
        skip 'not a debian-like system'
    fi
    load "../lib/setup.sh"
    load "../lib/bats-file/load.bash"
    ./instance-data load
}

#----------

@test "dovecot: detect unit (fail)" {
    run -0 cscli setup detect
    run -0 jq -r '.setup | .[].detected_service' <(output)
    refute_line 'dovecot-systemd'
}

@test "dovecot: install" {
    run -0 deb-install dovecot-core
    run -0 sudo systemctl enable dovecot.service
}

@test "dovecot: detect unit (succeed)" {
    run -0 cscli setup detect
    run -0 jq -r '.setup | .[].detected_service' <(output)
    assert_line 'dovecot-systemd'
}

@test "dovecot: install detected collection" {
    run -0 cscli setup detect
    run -0 cscli setup install-hub <(output)
}
