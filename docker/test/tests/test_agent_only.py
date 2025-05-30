import secrets
import time
from http import HTTPStatus

import pytest

pytestmark = pytest.mark.docker


def test_split_lapi_agent(crowdsec, flavor: str) -> None:
    rand = str(secrets.randbelow(10000))
    lapiname = f"lapi-{rand}"
    agentname = f"agent-{rand}"

    lapi_env = {
        "AGENT_USERNAME": "testagent",
        "AGENT_PASSWORD": "testpassword",
    }

    agent_env = {
        "AGENT_USERNAME": "testagent",
        "AGENT_PASSWORD": "testpassword",
        "DISABLE_LOCAL_API": "true",
        "LOCAL_API_URL": f"http://{lapiname}:8080",
    }

    cs_lapi = crowdsec(name=lapiname, environment=lapi_env, flavor=flavor)
    cs_agent = crowdsec(name=agentname, environment=agent_env, flavor=flavor)

    with cs_lapi as lapi:
        lapi.wait_for_log("*CrowdSec Local API listening on *:8080*")
        lapi.wait_for_http(8080, "/health", want_status=HTTPStatus.OK)
        with cs_agent as agent:
            agent.wait_for_log("*Starting processing data*")
            res = agent.cont.exec_run("cscli lapi status")
            assert res.exit_code == 0
            stdout = res.output.decode()
            assert "You can successfully interact with Local API (LAPI)" in stdout


def test_unregister_on_exit(crowdsec, flavor: str) -> None:
    rand = str(secrets.randbelow(10000))
    lapiname = f"lapi-{rand}"
    agentname = f"agent-{rand}"

    lapi_env = {
        "AGENT_USERNAME": "testagent",
        "AGENT_PASSWORD": "testpassword",
    }

    agent_env = {
        "AGENT_USERNAME": "testagent",
        "AGENT_PASSWORD": "testpassword",
        "DISABLE_LOCAL_API": "true",
        "LOCAL_API_URL": f"http://{lapiname}:8080",
        "UNREGISTER_ON_EXIT": "true",
    }

    cs_lapi = crowdsec(name=lapiname, environment=lapi_env, flavor=flavor)
    cs_agent = crowdsec(name=agentname, environment=agent_env, flavor=flavor, stop_timeout=5)

    with cs_lapi as lapi:
        lapi.wait_for_log("*CrowdSec Local API listening on *:8080*")
        lapi.wait_for_http(8080, "/health", want_status=HTTPStatus.OK)

        res = lapi.cont.exec_run("cscli machines list")
        assert res.exit_code == 0
        # the machine is created in the lapi entrypoint
        assert "testagent" in res.output.decode()

        with cs_agent as agent:
            agent.wait_for_log("*Starting processing data*")
            res = agent.cont.exec_run("cscli lapi status")
            assert res.exit_code == 0
            stdout = res.output.decode()
            assert "You can successfully interact with Local API (LAPI)" in stdout

            res = lapi.cont.exec_run("cscli machines list")
            assert res.exit_code == 0
            assert "testagent" in res.output.decode()

        time.sleep(2)

        res = lapi.cont.exec_run("cscli machines list")
        assert res.exit_code == 0
        # and it's not there anymore
        assert "testagent" not in res.output.decode()
