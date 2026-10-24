package main

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name trino

import (
	"context"
	"flag"
	"log"

	"github.com/caberdo/terraform-provider-trino/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is injected at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/caberdo/trino",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
