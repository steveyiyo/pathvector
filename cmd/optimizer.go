package cmd

import (
	"fmt"
	"io/ioutil"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/natesales/pathvector/internal/optimizer"
	"github.com/natesales/pathvector/internal/process"
)

func init() {
	rootCmd.AddCommand(optimizerCmd)
}

var optimizerCmd = &cobra.Command{
	Use:   "optimizer",
	Short: "Start optimization daemon",
	Run: func(cmd *cobra.Command, args []string) {
		log.Debugf("Loading config from %s", configFile)
		configFile, err := ioutil.ReadFile(configFile)
		if err != nil {
			log.Fatal("Reading config file: " + err.Error())
		}
		c, err := process.Load(configFile)
		if err != nil {
			log.Fatal(err)
		}
		log.Debugln("Finished loading config")

		log.Infof("Starting optimizer")
		sourceMap := map[string][]string{} // Peer name to list of source addresses
		for peerName, peerData := range c.Peers {
			if peerData.OptimizerProbeSources != nil && len(*peerData.OptimizerProbeSources) > 0 {
				sourceMap[fmt.Sprintf("%d%s%s", *peerData.ASN, optimizer.Delimiter, peerName)] = *peerData.OptimizerProbeSources
			}
		}
		log.Debugf("Optimizer probe sources: %v", sourceMap)
		if len(sourceMap) == 0 {
			log.Fatal("No peers have optimization enabled, exiting now")
		}
		if err := optimizer.StartProbe(&c.Optimizer, sourceMap, c, noConfigure, dryRun); err != nil {
			log.Fatal(err)
		}
	},
}
