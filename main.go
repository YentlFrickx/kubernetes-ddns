package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chyeh/pubip"
	"github.com/cloudflare/cloudflare-go"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"net"
	"os"
	"strings"
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
	zoneIdentifier := cloudflare.ZoneIdentifier(os.Getenv("CF_ZONE_ID"))

	records, _, err := c.CloudflareApi.ListDNSRecords(
		context.Background(),
		zoneIdentifier,
		cloudflare.ListDNSRecordsParams{Name: domain + "." + c.Tld})
	if err != nil {
		log.Errorln(err)
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
			log.Errorln(err)
		}
		log.Infoln("Created dns entry for " + domain)
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
				log.Errorln(err)
			}
			log.Infoln("Updated dns entry for " + domain)
		} else {
			log.Warn("Domain not managed by ddns " + domain)
		}

	}
}

func (c *CloudflareUpdater) extractHostnames(jsonData string) ([]string, error) {
	log.Infoln("Extracting hostnames from data")
	var data []map[string]interface{}
	err := json.Unmarshal([]byte(jsonData), &data)
	if err != nil {
		return nil, err
	}

	var hostnames []string
	for _, entry := range data {
		if rule, ok := entry["rule"].(string); ok {
			if host, err := c.extractHostname(rule); err == nil {
				if strings.HasSuffix(host, "."+c.Tld) {
					hostnames = append(hostnames, strings.TrimSuffix(host, "."+c.Tld))
				}
			}
		}
	}
	return hostnames, nil
}

func (c *CloudflareUpdater) extractHostname(rule string) (string, error) {
	// split rule on backticks
	parts := []rune(rule)
	for i := 0; i < len(parts); i++ {
		if parts[i] == '`' {
			// extract the string between the backticks
			j := i + 1
			for ; j < len(parts) && parts[j] != '`'; j++ {
			}
			if j > i+1 {
				return string(parts[i+1 : j]), nil
			}
		}
	}
	return "", fmt.Errorf("No hostname found in rule '%s'", rule)
}

func (c *CloudflareUpdater) updateHostnames() {
	ip, err := pubip.Get()
	if err != nil {
		fmt.Println("Couldn't get my IP address:", err)
	}

	selector := labels.Set{"cloudflare-ddns/hostname": ""}.AsSelector()

	ingresses, err := c.ClientSet.NetworkingV1beta1().Ingresses("").List(context.Background(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Printf("No Ingress resources found in the cluster")
			return
		}
		log.Error("Failed to list Ingress resources: %v", err)
	}

	for _, ingress := range ingresses.Items {
		hostname, _ := ingress.Annotations["external-dns.alpha.kubernetes.io/hostname"]
		c.updateDomain(hostname, ip)
	}
}

func main() {
	api, err := cloudflare.NewWithAPIToken(os.Getenv("CF_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}

	config, _ := rest.InClusterConfig()
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
