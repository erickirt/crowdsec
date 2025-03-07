#!/usr/bin/env bats

set -u

setup_file() {
    load "../lib/setup_file.sh"
    ./instance-data load
    INDEX_PATH=$(config_get '.config_paths.index_path')
    export INDEX_PATH
    CONFIG_DIR=$(config_get '.config_paths.config_dir')
    export CONFIG_DIR
}

teardown_file() {
    load "../lib/teardown_file.sh"
}

setup() {
    load "../lib/setup.sh"
    load "../lib/bats-file/load.bash"
    ./instance-data load
}

teardown() {
    ./instance-crowdsec stop
}

#----------

@test "cscli collections (dependencies)" {
    # inject a dependency: smb requires sshd
    hub_dep=$(jq <"$INDEX_PATH" '. * {collections:{"crowdsecurity/smb":{collections:["crowdsecurity/sshd"]}}}')
    echo "$hub_dep" >"$INDEX_PATH"

    # verify that installing smb brings sshd
    rune -0 cscli collections install crowdsecurity/smb
    rune -0 cscli collections list -o json
    rune -0 jq -e '[.collections[].name]==["crowdsecurity/smb","crowdsecurity/sshd"]' <(output)

    # verify that removing smb removes sshd too
    rune -0 cscli collections remove crowdsecurity/smb
    rune -0 cscli collections list -o json
    rune -0 jq -e '.collections | length == 0' <(output)

    # we can't remove sshd without --force
    rune -0 cscli collections install crowdsecurity/smb
    # XXX: should this be an error?
    rune -0 cscli collections remove crowdsecurity/sshd
    assert_stderr --partial "crowdsecurity/sshd belongs to collections: [crowdsecurity/smb]"
    assert_stderr --partial "Run 'sudo cscli collections remove crowdsecurity/sshd --force' if you want to force remove this collection"
    rune -0 cscli collections list -o json
    rune -0 jq -c '[.collections[].name]' <(output)
    assert_json '["crowdsecurity/smb","crowdsecurity/sshd"]'

    # use the --force
    rune -0 cscli collections remove crowdsecurity/sshd --force
    rune -0 cscli collections list -o json
    rune -0 jq -c '[.collections[].name]' <(output)
    assert_json '["crowdsecurity/smb"]'

    # and now smb is tainted!
    rune -0 cscli collections inspect crowdsecurity/smb -o json
    rune -0 jq -e '.tainted==true' <(output)
    rune -0 cscli collections remove crowdsecurity/smb --force

    # empty
    rune -0 cscli collections list -o json
    rune -0 jq -e '.collections | length == 0' <(output)

    # reinstall
    rune -0 cscli collections install crowdsecurity/smb --force

    # taint on sshd means smb is tainted as well
    rune -0 cscli collections inspect crowdsecurity/smb -o json
    rune -0 jq -e '.tainted==false' <(output)
    echo "dirty" >"$CONFIG_DIR/collections/sshd.yaml"
    rune -0 cscli collections inspect crowdsecurity/smb -o json
    rune -0 jq -e '.tainted==true' <(output)

    # now we can't remove smb without --force
    rune -1 cscli collections remove crowdsecurity/smb
    assert_stderr --partial "crowdsecurity/smb is tainted, use '--force' to remove"
}

@test "cscli collections inspect (dependencies)" {
    rune -0 cscli collections install crowdsecurity/smb

    # The inspect command must show the dependencies of the local or older version.
    echo "{'collections': ['crowdsecurity/sshd']}" >"$CONFIG_DIR/collections/smb.yaml"

    rune -0 cscli collections inspect crowdsecurity/smb --no-metrics -o json
    rune -0 jq -e '.collections' <(output)
    assert_json '["crowdsecurity/sshd"]'
}

@test "cscli collections (dependencies II: the revenge)" {
    rune -0 cscli collections install crowdsecurity/wireguard baudneo/gotify
    rune -0 cscli collections remove crowdsecurity/wireguard
    assert_output --regexp 'disabling collections:crowdsecurity/wireguard'
    refute_output --regexp 'disabling parsers:crowdsecurity/syslog-logs'
    rune -0 cscli collections inspect crowdsecurity/wireguard -o json
    rune -0 jq -e '.installed==false' <(output)
    rune -0 cscli parsers inspect crowdsecurity/syslog-logs -o json
    rune -0 jq -e '.installed==true' <(output)
}

@test "cscli collections (dependencies III: origins)" {
    # it is perfectly fine to remove an item belonging to a collection that we are removing anyway

    # inject a direct dependency: sshd requires the syslog-logs parsers, but linux does too
    hub_dep=$(jq <"$INDEX_PATH" '. * {collections:{"crowdsecurity/sshd":{parsers:["crowdsecurity/syslog-logs"]}}}')
    echo "$hub_dep" >"$INDEX_PATH"

    # verify that installing sshd brings syslog-logs
    rune -0 cscli collections install crowdsecurity/sshd
    rune -0 cscli parsers inspect crowdsecurity/syslog-logs -o json
    rune -0 jq -e '.installed==true' <(output)

    rune -0 cscli collections install crowdsecurity/linux

    # removing linux should remove syslog-logs even though sshd depends on it
    rune -0 cscli collections remove crowdsecurity/linux
    rune -0 cscli hub list -o json
    rune -0 jq -e 'add | length == 0' <(output)
}

@test "cscli collections (dependencies IV: looper)" {
    hub_dep=$(jq <"$INDEX_PATH" '. * {collections:{"crowdsecurity/sshd":{collections:["crowdsecurity/linux"]}}}')
    echo "$hub_dep" >"$INDEX_PATH"

    rune -1 cscli hub list
    assert_stderr --partial "circular dependency detected"
    rune -1 wait-for "$CROWDSEC"
    assert_stderr --partial "circular dependency detected"
}
