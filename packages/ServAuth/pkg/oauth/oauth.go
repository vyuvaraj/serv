package oauth

type ClientCredentials struct {
	ClientID     string
	ClientSecret string
}

func ValidateClient(clientID, clientSecret string, clients map[string]string) bool {
	secret, exists := clients[clientID]
	return exists && secret == clientSecret
}
