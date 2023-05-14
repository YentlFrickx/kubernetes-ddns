package main

import (
	"context"
	"github.com/chyeh/pubip"
	"github.com/cloudflare/cloudflare-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"net"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
)

type CloudflareUpdater struct {
	CloudflareApi *cloudflare.API
	ClientSet     *kubernetes.Clientset
	Tld           string
}

func boolPointer(b bool) *bool {
	return &b
}

func (c *CloudflareUpdater) updateDomain(domain string, ip net.IP) {
	log.Info().Msgf("Updating %s", domain)
	zoneIdentifier := cloudflare.ZoneIdentifier(os.Getenv("CF_ZONE_ID"))

	records, _, err := c.CloudflareApi.ListDNSRecords(
		context.Background(),
		zoneIdentifier,
		cloudflare.ListDNSRecordsParams{Name: domain + "." + c.Tld})
	if err != nil {
		log.Error().Err(err).Msg("Error listing dns records from cloudflare")
	} else if len(records) == 0 {
		params := cloudflare.CreateDNSRecordParams{
			Type:    "A",
			Name:    domain,
			Content: ip.String(),
			TTL:     1,
			Proxied: boolPointer(true),
			Comment: "Created from kubernetes",
		}
		_, err := c.CloudflareApi.CreateDNSRecord(context.Background(), zoneIdentifier, params)
		if err != nil {
			log.Error().Err(err).Msg("Error while creating DNS records")
		}
		log.Info().Msg("Created dns entry for " + domain)
	} else if len(records) > 0 && records[0].Content != ip.String() {
		if records[0].Comment == "Created from kubernetes" {
			params := cloudflare.UpdateDNSRecordParams{
				Type:    "A",
				Name:    domain,
				ID:      records[0].ID,
				Content: ip.String(),
				TTL:     1,
				Proxied: boolPointer(true),
				Comment: "Created from kubernetes",
			}
			_, err := c.CloudflareApi.UpdateDNSRecord(context.Background(), zoneIdentifier, params)
			if err != nil {
				log.Error().Err(err).Msg("Error while updating DNS records")
			}
			log.Info().Msg("Updated dns entry for " + domain)
		} else {
			log.Warn().Msg("Domain not managed by ddns " + domain)
		}

	} else {
		log.Debug().Msgf("Skipping domain since not modified %s", domain)
	}
}
func (c *CloudflareUpdater) updateHostnames() {
	ip, err := pubip.Get()
	if err != nil {
		log.Error().Err(err).Msg("Couldn't get my IP address")
	}

	ingresses, err := c.ClientSet.NetworkingV1().Ingresses("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Printf("No Ingress resources found in the cluster")
			return
		}
		log.Error().Err(err).Msg("Error fetching ingresses")
	}

	for _, ingress := range ingresses.Items {
		hostname, exists := ingress.Annotations["cloudflare-ddns/hostname"]
		if exists {
			c.updateDomain(hostname, ip)
		} else {
			log.Info().Msg("skipping ingress without annotation")
		}
	}
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Info().Msg("Starting ddns")

	api, err := cloudflare.NewWithAPIToken(os.Getenv("CF_TOKEN"))
	if err != nil {
		log.Fatal().Err(err).Msg("CF_TOKEN missing")
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := "./config"
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			log.Fatal().Msgf("Failed to get Kubernetes config: %v", err)
		}
	}
	clientset := kubernetes.NewForConfigOrDie(config)

	cloudflareUpdater := CloudflareUpdater{
		ClientSet:     clientset,
		CloudflareApi: api,
		Tld:           os.Getenv("TLD"),
	}

	for {
		cloudflareUpdater.updateHostnames()
		time.Sleep(60 * time.Second)
	}

}
