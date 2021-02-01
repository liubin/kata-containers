package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"k8s.io/client-go/kubernetes"
)

const (
	dockerConfigJsonKey    = ".dockerconfigjson"
	dockerConfigSecretType = "kubernetes.io/dockerconfigjson"
)

var (
	errNoRegistryFound = fmt.Errorf("no default register found")
	errNoAuthField     = fmt.Errorf("no auth field found in dockerconfigjson")
)

func main() {
	if len(os.Args) != 3 {
		panic("please use: regcred <namespace> <secret-name>")
	}

	namespace := os.Args[1]
	secretName := os.Args[2]

	cfg, err := rest.InClusterConfig()
	if err != nil {
		panic("failed to create k8s client config" + err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic("failed to create k8s client" + err.Error())
	}
	data, err := getCredentialsFromSecret(kubeClient, namespace, secretName)
	if err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "data: %+v", data)
	u, p, err := getAuthInfo(data)
	if err != nil {
		panic(err)
	}

	fmt.Print(u + ":" + p)
}

func getCredentialsFromSecret(kubeClient *kubernetes.Clientset, ns, secretName string) (map[string]string, error) {
	credentials := map[string]string{}
	secret, err := kubeClient.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return credentials, fmt.Errorf("failed to find the secret %s in the namespace %s with error: %v", secretName, ns, err)
	}

	if secret.Type != dockerConfigSecretType {
		// not a Docker config secret
		return credentials, fmt.Errorf("secret %s in the namespace %s is not a Docker config secret", secretName, ns)
	}

	for key, value := range secret.Data {
		credentials[key] = string(value)
	}

	return credentials, nil
}

func getAuthInfo(params map[string]string) (username string, password string, err error) {
	if v, found := params[dockerConfigJsonKey]; found {
		var decoded []byte

		decoded, err = base64.StdEncoding.DecodeString(string(v))
		if err != nil {
			// may be decoded content, so ignore decode error.
			decoded = []byte(v)
		}

		dockerCfg := &DockerConfigJson{}
		err = json.Unmarshal(decoded, dockerCfg)
		if err != nil {
			return
		}

		// check if has registries
		if len(dockerCfg.Auths) == 0 {
			err = errNoRegistryFound
			return
		}

		for _, defaultAuth := range dockerCfg.Auths {
			// check if has required "auth" field
			// https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/
			if defaultAuth.Auth == "" {
				err = errNoAuthField
				return
			}

			username, password, err = decodeDockerConfigFieldAuth(defaultAuth.Auth)
			// FIXME only processed the first auth info.
			return
		}
	} else {
		err = fmt.Errorf("no auth info found: %s", dockerConfigJsonKey)
	}
	return
}

// Copy from k8s.io/kubernetes/pkg/credentialprovider
type DockerConfigJson struct {
	Auths DockerConfig `json:"auths"`
}

// DockerConfig represents the config file used by the docker CLI.
// This config that represents the credentials that should be used
// when pulling images from specific image repositories.
type DockerConfig map[string]DockerConfigEntry

type DockerConfigEntry struct {
	// +optional
	Username string `json:"username,omitempty"`
	// +optional
	Password string `json:"password,omitempty"`
	// +optional
	Email string `json:"email,omitempty"`
	// +optional
	Auth string `json:"auth,omitempty"`
}

// decodeDockerConfigFieldAuth deserializes the "auth" field from dockercfg into a
// username and a password. The format of the auth field is base64(<username>:<password>).
func decodeDockerConfigFieldAuth(field string) (username, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(field)
	if err != nil {
		return
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("unable to parse auth field")
		return
	}

	username = parts[0]
	password = parts[1]

	return
}
