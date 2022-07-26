package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"jenkins_exporter/pkg/jenkins_exporter"

	"github.com/bndr/gojenkins"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	jenkinsUrl  = os.Getenv("JENKINS_HOST")
	jenkinsUser = os.Getenv("JENKINS_USER")
	jenkinsPass = os.Getenv("JENKINS_PASS")
	listenPort  = getEnvDefault("LISTEN_PORT", ":9001")
)

func getEnvDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func main() {
	ctx := context.Background()
	jenkins := gojenkins.CreateJenkins(nil, jenkinsUrl, jenkinsUser, jenkinsPass)

	_, err := jenkins.Init(ctx)
	if err != nil {
		log.Fatal(err)
	}

	exporter := jenkins_exporter.NewExporter(jenkins)
	go exporter.Run()

	prometheus.MustRegister(&exporter)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(listenPort, nil))
}
