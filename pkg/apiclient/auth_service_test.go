package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crowdsecurity/crowdsec/pkg/models"
)

type BasicMockPayload struct {
	MachineID string `json:"machine_id"`
	Password  string `json:"password"`
}

func getLoginsForMockErrorCases() map[string]int {
	return map[string]int{
		"login_400": http.StatusBadRequest,
		"login_409": http.StatusConflict,
		"login_500": http.StatusInternalServerError,
	}
}

func initBasicMuxMock(t *testing.T, mux *http.ServeMux, path string) {
	loginsForMockErrorCases := getLoginsForMockErrorCases()

	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")

		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		newStr := buf.String()

		var payload BasicMockPayload

		err := json.Unmarshal([]byte(newStr), &payload)
		if err != nil || payload.MachineID == "" || payload.Password == "" {
			log.Printf("Bad payload")
			w.WriteHeader(http.StatusBadRequest)
		}

		var responseBody string

		responseCode, hasFoundErrorMock := loginsForMockErrorCases[payload.MachineID]
		if !hasFoundErrorMock {
			responseCode = http.StatusOK
			responseBody = `{"code":200,"expire":"2029-11-30T14:14:24+01:00","token":"toto"}`
		} else {
			responseBody = fmt.Sprintf("Error %d", responseCode)
		}

		log.Printf("MockServerReceived > %s // Login : [%s] => Mux response [%d]", newStr, payload.MachineID, responseCode)

		w.WriteHeader(responseCode)
		fmt.Fprintf(w, `%s`, responseBody)
	})
}

/**
 * Test the RegisterClient function
 * Making sure it handles the different response code potentially coming from CAPI properly
 * 200 => OK
 * 400, 409, 500 => Error
 */
func TestWatcherRegister(t *testing.T) {
	ctx := t.Context()

	log.SetLevel(log.DebugLevel)

	mux, urlx, teardown := setup()
	defer teardown()

	// body: models.WatcherRegistrationRequest{MachineID: &config.MachineID, Password: &config.Password}
	initBasicMuxMock(t, mux, "/watchers")
	log.Printf("URL is %s", urlx)

	apiURL, err := url.Parse(urlx + "/")
	require.NoError(t, err)

	// Valid Registration : should retrieve the client and no err
	clientconfig := Config{
		MachineID:     "test_login",
		Password:      "test_password",
		URL:           apiURL,
		VersionPrefix: "v1",
	}

	client, err := RegisterClient(ctx, &clientconfig, &http.Client{})
	require.NoError(t, err)

	log.Printf("->%T", client)

	// Testing error handling on Registration (400, 409, 500): should retrieve an error
	errorCodesToTest := [3]int{http.StatusBadRequest, http.StatusConflict, http.StatusInternalServerError}
	for _, errorCodeToTest := range errorCodesToTest {
		clientconfig.MachineID = fmt.Sprintf("login_%d", errorCodeToTest)

		client, err = RegisterClient(ctx, &clientconfig, &http.Client{})
		require.Nil(t, client, "nil expected for the response code %d", errorCodeToTest)
		require.Error(t, err, "error expected for the response code %d", errorCodeToTest)
	}
}

func TestWatcherAuth(t *testing.T) {
	ctx := t.Context()

	log.SetLevel(log.DebugLevel)

	mux, urlx, teardown := setup()
	defer teardown()
	// body: models.WatcherRegistrationRequest{MachineID: &config.MachineID, Password: &config.Password}

	initBasicMuxMock(t, mux, "/watchers/login")
	log.Printf("URL is %s", urlx)

	apiURL, err := url.Parse(urlx + "/")
	require.NoError(t, err)

	updateScenario := func(_ context.Context) ([]string, error) {
		return []string{"crowdsecurity/test"}, nil
	}

	// ok auth
	clientConfig := &Config{
		MachineID:      "test_login",
		Password:       "test_password",
		URL:            apiURL,
		VersionPrefix:  "v1",
		UpdateScenario: updateScenario,
	}

	client, err := NewClient(clientConfig)
	require.NoError(t, err)

	scenarios, err := clientConfig.UpdateScenario(ctx)
	require.NoError(t, err)

	_, _, err = client.Auth.AuthenticateWatcher(ctx, models.WatcherAuthRequest{
		MachineID: &clientConfig.MachineID,
		Password:  &clientConfig.Password,
		Scenarios: scenarios,
	})
	require.NoError(t, err)

	// Testing error handling on AuthenticateWatcher (400, 409): should retrieve an error
	// Not testing 500 because it loops and try to re-autehnticate. But you can test it manually by adding it in array
	errorCodesToTest := [2]int{http.StatusBadRequest, http.StatusConflict}
	for _, errorCodeToTest := range errorCodesToTest {
		clientConfig.MachineID = fmt.Sprintf("login_%d", errorCodeToTest)

		client, err := NewClient(clientConfig)
		require.NoError(t, err)

		_, resp, err := client.Auth.AuthenticateWatcher(ctx, models.WatcherAuthRequest{
			MachineID: &clientConfig.MachineID,
			Password:  &clientConfig.Password,
		})

		if err == nil {
			resp.Response.Body.Close()

			bodyBytes, err := io.ReadAll(resp.Response.Body)
			require.NoError(t, err)

			log.Print(string(bodyBytes))
			t.Fatalf("The AuthenticateWatcher function should have returned an error for the response code %d", errorCodeToTest)
		}

		log.Printf("The AuthenticateWatcher function handled the error code %d as expected \n\r", errorCodeToTest)
	}
}

func TestWatcherUnregister(t *testing.T) {
	ctx := t.Context()

	log.SetLevel(log.DebugLevel)

	mux, urlx, teardown := setup()
	defer teardown()
	// body: models.WatcherRegistrationRequest{MachineID: &config.MachineID, Password: &config.Password}

	mux.HandleFunc("/watchers/self", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		assert.Equal(t, int64(0), r.ContentLength)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/watchers/login", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")

		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)

		newStr := buf.String()
		if newStr == `{"machine_id":"test_login","password":"test_password","scenarios":["crowdsecurity/test"]}
` {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"code":200,"expire":"2029-11-30T14:14:24+01:00","token":"toto"}`)
		} else {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"message":"access forbidden"}`)
		}
	})

	log.Printf("URL is %s", urlx)

	apiURL, err := url.Parse(urlx + "/")
	require.NoError(t, err)

	updateScenario := func(_ context.Context) ([]string, error) {
		return []string{"crowdsecurity/test"}, nil
	}

	mycfg := &Config{
		MachineID:      "test_login",
		Password:       "test_password",
		URL:            apiURL,
		VersionPrefix:  "v1",
		UpdateScenario: updateScenario,
	}

	client, err := NewClient(mycfg)
	require.NoError(t, err)

	_, err = client.Auth.UnregisterWatcher(ctx)
	require.NoError(t, err)

	log.Printf("->%T", client)
}

func TestWatcherEnroll(t *testing.T) {
	ctx := t.Context()

	log.SetLevel(log.DebugLevel)

	mux, urlx, teardown := setup()
	defer teardown()

	mux.HandleFunc("/watchers/enroll", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")

		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		newStr := buf.String()
		log.Debugf("body -> %s", newStr)

		if newStr == `{"attachment_key":"goodkey","name":"","tags":[],"overwrite":false}
` {
			log.Print("good key")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"statusCode": 200, "message": "OK"}`)
		} else {
			log.Print("bad key")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"message":"the attachment key provided is not valid"}`)
		}
	})

	mux.HandleFunc("/watchers/login", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"code":200,"expire":"2029-11-30T14:14:24+01:00","token":"toto"}`)
	})

	log.Printf("URL is %s", urlx)

	apiURL, err := url.Parse(urlx + "/")
	require.NoError(t, err)

	updateScenario := func(_ context.Context) ([]string, error) {
		return []string{"crowdsecurity/test"}, nil
	}

	mycfg := &Config{
		MachineID:      "test_login",
		Password:       "test_password",
		URL:            apiURL,
		VersionPrefix:  "v1",
		UpdateScenario: updateScenario,
	}

	client, err := NewClient(mycfg)
	require.NoError(t, err)

	_, err = client.Auth.EnrollWatcher(ctx, "goodkey", "", []string{}, false)
	require.NoError(t, err)

	_, err = client.Auth.EnrollWatcher(ctx, "badkey", "", []string{}, false)
	assert.Contains(t, err.Error(), "the attachment key provided is not valid", "got %s", err.Error())
}
