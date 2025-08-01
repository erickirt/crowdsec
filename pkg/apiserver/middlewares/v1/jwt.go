package v1

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-openapi/strfmt"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"

	"github.com/crowdsecurity/crowdsec/pkg/database"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent/machine"
	"github.com/crowdsecurity/crowdsec/pkg/models"
	"github.com/crowdsecurity/crowdsec/pkg/types"
)

const MachineIDKey = "id"

type JWT struct {
	Middleware *jwt.GinJWTMiddleware
	DbClient   *database.Client
	TlsAuth    *TLSAuth
}

func PayloadFunc(data any) jwt.MapClaims {
	if value, ok := data.(*models.WatcherAuthRequest); ok {
		return jwt.MapClaims{
			MachineIDKey: &value.MachineID,
		}
	}

	return jwt.MapClaims{}
}

func IdentityHandler(c *gin.Context) any {
	claims := jwt.ExtractClaims(c)
	machineID := claims[MachineIDKey].(string)

	return &models.WatcherAuthRequest{
		MachineID: &machineID,
	}
}

type authInput struct {
	machineID      string
	clientMachine  *ent.Machine
	scenariosInput []string
}

func (j *JWT) authTLS(c *gin.Context) (*authInput, error) {
	ctx := c.Request.Context()
	ret := authInput{}

	if j.TlsAuth == nil {
		err := errors.New("tls authentication required")
		log.Warn(err)

		return nil, err
	}

	extractedCN, err := j.TlsAuth.ValidateCert(c)
	if err != nil {
		log.Warn(err)
		return nil, err
	}

	logger := log.WithField("ip", c.ClientIP())

	ret.machineID = fmt.Sprintf("%s@%s", extractedCN, c.ClientIP())

	ret.clientMachine, err = j.DbClient.Ent.Machine.Query().
		Where(machine.MachineId(ret.machineID)).
		First(ctx)
	if ent.IsNotFound(err) {
		// Machine was not found, let's create it
		logger.Infof("machine %s not found, create it", ret.machineID)
		// let's use an apikey as the password, doesn't matter in this case (generatePassword is only available in cscli)
		pwd, err := GenerateAPIKey(dummyAPIKeySize)
		if err != nil {
			logger.WithField("cn", extractedCN).
				Errorf("error generating password: %s", err)

			return nil, errors.New("error generating password")
		}

		password := strfmt.Password(pwd)

		ret.clientMachine, err = j.DbClient.CreateMachine(ctx, &ret.machineID, &password, "", true, true, types.TlsAuthType)
		if err != nil {
			return nil, fmt.Errorf("while creating machine entry for %s: %w", ret.machineID, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("while selecting machine entry for %s: %w", ret.machineID, err)
	} else {
		if ret.clientMachine.AuthType != types.TlsAuthType {
			return nil, fmt.Errorf("machine %s attempted to auth with TLS cert but it is configured to use %s", ret.machineID, ret.clientMachine.AuthType)
		}

		ret.machineID = ret.clientMachine.MachineId
	}

	loginInput := struct {
		Scenarios []string `json:"scenarios"`
	}{
		Scenarios: []string{},
	}

	err = c.ShouldBindJSON(&loginInput)
	if err != nil {
		return nil, fmt.Errorf("missing scenarios list in login request for TLS auth: %w", err)
	}

	ret.scenariosInput = loginInput.Scenarios

	return &ret, nil
}

func (j *JWT) authPlain(c *gin.Context) (*authInput, error) {
	var (
		loginInput models.WatcherAuthRequest
		err        error
	)

	ctx := c.Request.Context()

	ret := authInput{}

	if err = c.ShouldBindJSON(&loginInput); err != nil {
		return nil, fmt.Errorf("missing: %w", err)
	}

	if err = loginInput.Validate(strfmt.Default); err != nil {
		return nil, err
	}

	ret.machineID = *loginInput.MachineID
	password := *loginInput.Password
	ret.scenariosInput = loginInput.Scenarios

	ret.clientMachine, err = j.DbClient.Ent.Machine.Query().
		Where(machine.MachineId(ret.machineID)).
		First(ctx)
	if err != nil {
		log.Infof("Error machine login for %s : %+v ", ret.machineID, err)
		return nil, err
	}

	if ret.clientMachine == nil {
		log.Errorf("Nothing for '%s'", ret.machineID)
		return nil, jwt.ErrFailedAuthentication
	}

	if ret.clientMachine.AuthType != types.PasswordAuthType {
		return nil, fmt.Errorf("machine %s attempted to auth with password but it is configured to use %s", ret.machineID, ret.clientMachine.AuthType)
	}

	if !ret.clientMachine.IsValidated {
		return nil, fmt.Errorf("machine %s not validated", ret.machineID)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(ret.clientMachine.Password), []byte(password)); err != nil {
		return nil, jwt.ErrFailedAuthentication
	}

	return &ret, nil
}

func (j *JWT) Authenticator(c *gin.Context) (any, error) {
	var (
		err  error
		auth *authInput
	)

	ctx := c.Request.Context()

	if c.Request.TLS != nil && len(c.Request.TLS.PeerCertificates) > 0 {
		auth, err = j.authTLS(c)
		if err != nil {
			return nil, err
		}
	} else {
		auth, err = j.authPlain(c)
		if err != nil {
			return nil, err
		}
	}

	var scenarios string

	if len(auth.scenariosInput) > 0 {
		for _, scenario := range auth.scenariosInput {
			if scenarios == "" {
				scenarios = scenario
			} else {
				scenarios += "," + scenario
			}
		}

		err = j.DbClient.UpdateMachineScenarios(ctx, scenarios, auth.clientMachine.ID)
		if err != nil {
			log.Errorf("Failed to update scenarios list for '%s': %s\n", auth.machineID, err)
			return nil, jwt.ErrFailedAuthentication
		}
	}

	clientIP := c.ClientIP()

	if auth.clientMachine.IpAddress == "" {
		err = j.DbClient.UpdateMachineIP(ctx, clientIP, auth.clientMachine.ID)
		if err != nil {
			log.Errorf("Failed to update ip address for '%s': %s\n", auth.machineID, err)
			return nil, jwt.ErrFailedAuthentication
		}
	}

	if auth.clientMachine.IpAddress != clientIP && auth.clientMachine.IpAddress != "" {
		log.Warningf("new IP address detected for machine '%s': %s (old: %s)", auth.clientMachine.MachineId, clientIP, auth.clientMachine.IpAddress)

		err = j.DbClient.UpdateMachineIP(ctx, clientIP, auth.clientMachine.ID)
		if err != nil {
			log.Errorf("Failed to update ip address for '%s': %s\n", auth.clientMachine.MachineId, err)
			return nil, jwt.ErrFailedAuthentication
		}
	}

	useragent := strings.Split(c.Request.UserAgent(), "/")
	if len(useragent) != 2 {
		log.Warningf("bad user agent '%s' from '%s'", c.Request.UserAgent(), clientIP)
		return nil, jwt.ErrFailedAuthentication
	}

	if err := j.DbClient.UpdateMachineVersion(ctx, useragent[1], auth.clientMachine.ID); err != nil {
		log.Errorf("unable to update machine '%s' version '%s': %s", auth.clientMachine.MachineId, useragent[1], err)
		return nil, jwt.ErrFailedAuthentication
	}

	return &models.WatcherAuthRequest{
		MachineID: &auth.machineID,
	}, nil
}

func Authorizator(data any, c *gin.Context) bool {
	return true
}

func Unauthorized(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"code":    code,
		"message": message,
	})
}

func randomSecret() ([]byte, error) {
	size := 64
	secret := make([]byte, size)

	n, err := rand.Read(secret)
	if err != nil {
		return nil, errors.New("unable to generate a new random seed for JWT generation")
	}

	if n != size {
		return nil, errors.New("not enough entropy at random seed generation for JWT generation")
	}

	return secret, nil
}

func NewJWT(dbClient *database.Client) (*JWT, error) {
	// Get secret from environment variable "SECRET"
	var (
		secret []byte
		err    error
	)

	// Please be aware that brute force HS256 is possible.
	// PLEASE choose a STRONG secret
	secretString := os.Getenv("CS_LAPI_SECRET")
	secret = []byte(secretString)

	switch l := len(secret); {
	case l == 0:
		secret, err = randomSecret()
		if err != nil {
			return &JWT{}, err
		}
	case l < 64:
		return &JWT{}, errors.New("CS_LAPI_SECRET not strong enough")
	}

	jwtMiddleware := &JWT{
		DbClient: dbClient,
		TlsAuth:  &TLSAuth{},
	}

	ret, err := jwt.New(&jwt.GinJWTMiddleware{
		Realm:           "Crowdsec API local",
		Key:             secret,
		Timeout:         time.Hour,
		MaxRefresh:      time.Hour,
		IdentityKey:     MachineIDKey,
		PayloadFunc:     PayloadFunc,
		IdentityHandler: IdentityHandler,
		Authenticator:   jwtMiddleware.Authenticator,
		Authorizator:    Authorizator,
		Unauthorized:    Unauthorized,
		TokenLookup:     "header: Authorization, query: token, cookie: jwt",
		TokenHeadName:   "Bearer",
		TimeFunc:        time.Now,
	})
	if err != nil {
		return &JWT{}, err
	}

	errInit := ret.MiddlewareInit()
	if errInit != nil {
		return &JWT{}, errors.New("authMiddleware.MiddlewareInit() Error:" + errInit.Error())
	}

	jwtMiddleware.Middleware = ret

	return jwtMiddleware, nil
}
