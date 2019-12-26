package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/benjivesterby/validator"
	"github.com/nortonlifelock/config"
	"github.com/nortonlifelock/database"
	"github.com/nortonlifelock/domain"
	"github.com/nortonlifelock/endpoints"
	"github.com/rs/cors"
)

//TODO update last seen time of user that's authenticated
//TODO need to verify the organization for the change is the same for the organization of the user

var (
	apiPort int
)

func main() {
	var err error
	endpoints.SigningKey, err = generateSigningKey(256)
	if err == nil {
		fmt.Println("Signing key: " + endpoints.SigningKey)
		fmt.Printf("Listening on port %d...\n", apiPort)
		router := endpoints.NewRouter()
		c := cors.New(cors.Options{
			//AllowedOrigins doesn't have to be the server URL, localhost is fine because nginx uses a reverse proxy
			AllowedOrigins:   []string{fmt.Sprintf("%s://%s", endpoints.AppConfig.TransportProtocol(), endpoints.AppConfig.UILocation())},
			AllowedMethods:   []string{http.MethodHead, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
			AllowedHeaders:   []string{"*"},
			AllowCredentials: true,
		})
		handler := c.Handler(router)

		err = http.ListenAndServe(fmt.Sprintf(":%d", apiPort), handler)
		log.Fatal(err)
	}
}

func loadOrganizationADSettings(db domain.DatabaseConnection) (orgIDToWrapper map[string]*endpoints.OrgConfigWrapper, err error) {
	var idToOrg = make(map[string]domain.Organization)
	var rootOrgs = make([]domain.Organization, 0)
	orgIDToWrapper = make(map[string]*endpoints.OrgConfigWrapper, 0)

	var orgs []domain.Organization
	orgs, err = db.GetOrganizations()
	if err == nil {
		// we do a first pass over the orgs to grab the root organizations, and to map the orgs by their IDs
		for index := range orgs {
			org := orgs[index]

			idToOrg[org.ID()] = org

			if org.ParentOrgID() == nil {
				rootOrgs = append(rootOrgs, org)
			}
		}

		if len(rootOrgs) > 0 {
			// we load the AD configuration from the organizations that are at the root of a hierarchy
			for _, rootOrg := range rootOrgs {
				con := &endpoints.ADConfig{}
				if err = json.Unmarshal([]byte(rootOrg.Payload()), con); err == nil {
					orgIDToWrapper[rootOrg.ID()] = &endpoints.OrgConfigWrapper{
						Org: rootOrg,
						Con: con,
					}
				} else {
					break
				}
			}

			// now we tie the child organizations with the configs of their root organizations
			for _, org := range orgs {
				if orgIDToWrapper[org.ID()] == nil {

					// grab the root org using traverse
					var traverse = org
					for traverse != nil && traverse.ParentOrgID() != nil {
						traverse = idToOrg[*traverse.ParentOrgID()]
					}

					if traverse != nil {
						orgIDToWrapper[org.ID()] = &endpoints.OrgConfigWrapper{
							Org: org,
							Con: orgIDToWrapper[traverse.ID()].Con,
						}
					} else {
						err = fmt.Errorf("could not find root organization for %s", org.ID())
						break
					}
				}
			}
		} else {
			err = fmt.Errorf("could not find a root organization")
		}
	}

	return orgIDToWrapper, err
}

func generateSigningKey(keyLength int) (string, error) {
	var retVal string
	b := make([]byte, keyLength)
	_, err := rand.Read(b)
	if err == nil {
		retVal = base64.URLEncoding.EncodeToString(b)
	}
	return retVal, err
}

func init() {
	path := flag.String("p", "", "")
	file := flag.String("f", "", "")
	flag.Parse()

	if path != nil && file != nil && len(*path) > 0 && len(*file) > 0 {

		if appConfig, err := config.LoadConfig(*path, *file); err == nil {
			if validator.IsValid(appConfig) {

				endpoints.AppConfig = appConfig

				var dbConn domain.DatabaseConnection
				var err error
				if dbConn, err = database.NewConnection(appConfig); err == nil {
					endpoints.Ms = dbConn.(domain.DatabaseConnection)
					if endpoints.OrgADConfigs, err = loadOrganizationADSettings(dbConn); err != nil {
						panic("error while loading AD organization information - " + err.Error())
					}
				} else {
					panic("Error while opening database connection " + err.Error())
				}

				if appConfig.APIPort() > 0 {

					apiPort = appConfig.APIPort()

					if len(appConfig.EncryptionKey()) > 0 {
						endpoints.EncryptionKey = appConfig.EncryptionKey()

						if len(appConfig.AegisPath()) > 0 {
							endpoints.WorkingDir = appConfig.AegisPath()
						} else {
							panic("app.json must supply a path_to_aegis")
						}
					} else {
						panic("app.json must supply a key_id for KMS")
					}

				} else {
					panic("app.json must supply an api_port to listen on")
				}

			} else {
				panic("Invalid app.json")
			}
		} else {
			panic("Could not load app.json")
		}
	} else {
		panic("A path to the app.json must be supplied with the -p flag, " +
			" the config file name must be supplied with the -f flag")
	}
}
