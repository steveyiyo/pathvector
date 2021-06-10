package main

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

//go:generate ./generate.sh

var cliFlags struct {
	ConfigFile            string `short:"c" long:"config" description:"Configuration file in YAML, TOML, or JSON format" default:"/etc/wireframe.yml"`
	Verbose               bool   `short:"v" long:"verbose" description:"Show verbose log messages"`
	DryRun                bool   `short:"d" long:"dry-run" description:"Don't modify configuration"`
	NoConfigure           bool   `short:"n" long:"no-configure" description:"Don't configure BIRD"`
	ShowVersion           bool   `short:"V" long:"version" description:"Show version and exit"`
	BirdDirectory         string `long:"bird-directory" description:"Directory to store BIRD configs" default:"/etc/bird/"`
	BirdSocket            string `long:"bird-socket" description:"UNIX control socket for BIRD" default:"/run/bird/bird.ctl"`
	KeepalivedConfig      string `long:"keepalived-config" description:"Configuration file for keepalived" default:"/etc/keepalived.conf"`
	WebUIFile             string `long:"web-ui-file" description:"File to write web UI to" default:"/run/wireframe.html"`
	PeeringDbQueryTimeout uint   `long:"peeringdb-query-timeout" description:"PeeringDB query timeout in seconds" default:"10"`
	IRRQueryTimeout       uint   `long:"irr-query-timeout" description:"IRR query timeout in seconds" default:"30"`
}

type peer struct {
	Template *string `yaml:"template" description:"Configuration template" default:"-"`

	Description *string `yaml:"description" description:"Peer description" default:"-"`
	Disabled    *bool   `yaml:"disabled" description:"Should the sessions be disabled?" default:"false"`

	// BGP Attributes
	ASN               *uint     `yaml:"asn" description:"Local ASN" validate:"required" default:"0"`
	NeighborIPs       *[]string `yaml:"neighbors" description:"List of neighbor IPs" validate:"required,ip" default:"-"`
	Prepends          *uint     `yaml:"prepends" description:"Number of times to prepend local AS on export" default:"0"`
	LocalPref         *uint     `yaml:"local-pref" description:"BGP local preference" default:"100"`
	Multihop          *bool     `yaml:"multihop" description:"Should BGP multihop be enabled? (255 max hops)" default:"false"`
	Listen            *string   `yaml:"listen" description:"BGP listen address" default:"-"`
	LocalPort         *uint     `yaml:"local-port" description:"Local TCP port" default:"179"`
	NeighborPort      *uint     `yaml:"neighbor-port" description:"Neighbor TCP port" default:"179"`
	Passive           *bool     `yaml:"passive" description:"Should we listen passively?" default:"false"`
	NextHopSelf       *bool     `yaml:"next-hop-self" description:"Should BGP next-hop-self be enabled?" default:"false"`
	BFD               *bool     `yaml:"bfd" description:"Should BFD be enabled?" default:"false"`
	Password          *string   `yaml:"password" description:"BGP MD5 password" default:"-"`
	RSClient          *bool     `yaml:"rs-client" description:"Should this peer be a route server client?" default:"false"`
	RRClient          *bool     `yaml:"rr-client" description:"Should this peer be a route reflector client?" default:"false"`
	RemovePrivateASNs *bool     `yaml:"remove-private-as" description:"Should private ASNs be removed from path before exporting?" default:"true"`
	MPUnicast46       *bool     `yaml:"mp-unicast-46" description:"Should this peer be configured with multiprotocol IPv4 and IPv6 unicast?" default:"false"`
	AllowLocalAS      *bool     `yaml:"allow-local-as" description:"Should routes originated by the local ASN be accepted?" default:"false"`

	ImportCommunities   *[]string `yaml:"import-communities" description:"List of communities to add to all imported routes" default:"-"`
	ExportCommunities   *[]string `yaml:"export-communities" description:"List of communities to add to all exported routes" default:"-"`
	AnnounceCommunities *[]string `yaml:"announce-communities" description:"Announce all routes matching these communities to the peer" default:"-"`

	// Filtering
	ASSet                   *string `yaml:"as-set" description:"Peer's as-set for filtering" default:"-"`
	ImportLimit4            *uint   `yaml:"import-limit4" description:"Maximum number of IPv4 prefixes to import" default:"1000000"`
	ImportLimit6            *uint   `yaml:"import-limit6" description:"Maximum number of IPv6 prefixes to import" default:"100000"`
	EnforceFirstAS          *bool   `yaml:"enforce-first-as" description:"Should we only accept routes who's first AS is equal to the configured peer address?" default:"true"`
	EnforcePeerNexthop      *bool   `yaml:"enforce-peer-nexthop" description:"Should we only accept routes with a next hop equal to the configured neighbor address?" default:"true"`
	MaxPrefixTripAction     *string `yaml:"max-prefix-action" description:"What action should be taken when the max prefix limit is tripped?" default:"disable"`
	AllowBlackholeCommunity *bool   `yaml:"allow-blackhole-community" description:"Should this peer be allowed to send routes with the blackhole community?" default:"false"`

	FilterIRR           *bool `yaml:"filter-irr" description:"Should IRR filtering be applied?" default:"true"`
	FilterRPKI          *bool `yaml:"filter-rpki" description:"Should RPKI invalids be rejected?" default:"true"`
	FilterMaxPrefix     *bool `yaml:"filter-max-prefix" description:"Should max prefix filtering be applied?" default:"true"`
	FilterBogons        *bool `yaml:"filter-bogons" description:"Should bogon prefixes be rejected?" default:"true"`
	FilterTier1ASNs     *bool `yaml:"filter-tier1-asns" description:"Should paths containing 'Tier 1' ASNs be rejected (Peerlock Lite)?'" default:"false"`
	FilterSmallPrefixes *bool `yaml:"filter-small-prefixes" description:"Should small prefixes (ge 24, ge 48) be rejected?" default:"true"`

	AutoImportLimits *bool `yaml:"auto-import-limits" description:"Get import limits automatically from PeeringDB?" default:"false"`
	AutoASSet        *bool `yaml:"auto-as-set" description:"Get as-set automatically from PeeringDB?" default:"false"`

	Prefixes *[]string `yaml:"prefixes" description:"Prefixes to accept" default:"-"`

	// Export options
	AnnounceDefault    *bool `yaml:"announce-default" description:"Should a default route be exported to this peer?" default:"false"`
	AnnounceOriginated *bool `yaml:"announce-originated" description:"Should locally originated routes be announced to this peer?" default:"true"`

	// Custom daemon configuration
	SessionGlobal  *string `yaml:"session-global" description:"Configuration to add to each session before any defined BGP protocols" default:"-"`
	PreImport      *string `yaml:"pre-import" description:"Configuration to add at the beginning of the import filter" default:"-"`
	PreExport      *string `yaml:"pre-export" description:"Configuration to add at the beginning of the export filter" default:"-"`
	PreImportFinal *string `yaml:"pre-import-final" description:"Configuration to add immediately before the final accept/reject on import" default:"-"`
	PreExportFinal *string `yaml:"pre-export-final" description:"Configuration to add immediately before the final accept/reject on export" default:"-"`

	ProtocolName                *string   `yaml:"-" description:"-" default:"-"`
	Protocols                   *[]string `yaml:"-" description:"-" default:"-"`
	PrefixSet4                  *[]string `yaml:"-" description:"-" default:"-"`
	PrefixSet6                  *[]string `yaml:"-" description:"-" default:"-"`
	ImportStandardCommunities   *[]string `yaml:"-" description:"-" default:"-"`
	ImportLargeCommunities      *[]string `yaml:"-" description:"-" default:"-"`
	ExportStandardCommunities   *[]string `yaml:"-" description:"-" default:"-"`
	ExportLargeCommunities      *[]string `yaml:"-" description:"-" default:"-"`
	AnnounceStandardCommunities *[]string `yaml:"-" description:"-" default:"-"`
	AnnounceLargeCommunities    *[]string `yaml:"-" description:"-" default:"-"`
}

type vrrpInstance struct {
	State     string   `yaml:"state" description:"VRRP instance state ('primary' or 'backup')" validate:"required"`
	Interface string   `yaml:"interface" description:"Interface to send VRRP packets on" validate:"required"`
	VRID      uint     `yaml:"vrid" description:"RFC3768 VRRP Virtual Router ID (1-255)" validate:"required"`
	Priority  uint     `yaml:"priority" description:"RFC3768 VRRP Priority" validate:"required"`
	VIPs      []string `yaml:"vips" description:"List of virtual IPs" validate:"required,cidr"`

	VIPs4 []string `yaml:"-" description:"-"`
	VIPs6 []string `yaml:"-" description:"-"`
}

type augments struct {
	Accept4 []string          `yaml:"accept4" description:"List of BIRD protocols to import into the IPv4 table"`
	Accept6 []string          `yaml:"accept6" description:"List of BIRD protocols to import into the IPv6 table"`
	Reject4 []string          `yaml:"reject4" description:"List of BIRD protocols to not import into the IPv4 table"`
	Reject6 []string          `yaml:"reject6" description:"List of BIRD protocols to not import into the IPv6 table"`
	Statics map[string]string `yaml:"statics" description:"List of static routes to include in BIRD"`

	Statics4 map[string]string `yaml:"-" description:"-"`
	Statics6 map[string]string `yaml:"-" description:"-"`
}

type config struct {
	ASN              uint     `yaml:"asn" description:"Autonomous System Number" validate:"required" default:"0"`
	Prefixes         []string `yaml:"prefixes" description:"List of prefixes to announce"`
	Communities      []string `yaml:"communities" description:"List of RFC1997 BGP communities"`
	LargeCommunities []string `yaml:"large-communities" description:"List of RFC8092 large BGP communities"`

	RouterID      string `yaml:"router-id" description:"Router ID (dotted quad notation)" validate:"required"`
	IRRServer     string `yaml:"irr-server" description:"Internet routing registry server" default:"rr.ntt.net"`
	RTRServer     string `yaml:"rtr-server" description:"RPKI-to-router server" default:"rtr.rpki.cloudflare.com"`
	RTRPort       uint   `yaml:"rtr-port" description:"RPKI-to-router port" default:"8282"`
	KeepFiltered  bool   `yaml:"keep-filtered" description:"Should filtered routes be kept in memory?" default:"false"`
	MergePaths    bool   `yaml:"merge-paths" description:"Should best and equivalent non-best routes be imported to build ECMP routes?" default:"false"`
	Source4       string `yaml:"source4" description:"Source IPv4 address"`
	Source6       string `yaml:"source6" description:"Source IPv6 address"`
	AcceptDefault bool   `yaml:"accept-default" description:"Should default routes be added to the bogon list?" default:"false"`

	Peers         map[string]*peer `yaml:"peers" description:"BGP peer configuration"`
	Templates     map[string]*peer `yaml:"templates" description:"BGP peer templates"`
	Interfaces    map[string]iface `yaml:"interfaces" description:"Network interface configuration"`
	VRRPInstances []vrrpInstance   `yaml:"vrrp" description:"List of VRRP instances"`
	Augments      augments         `yaml:"augments" description:"Custom configuration options"`

	Prefixes4 []string `yaml:"-" description:"-"`
	Prefixes6 []string `yaml:"-" description:"-"`
}

// iface represents a network interface
type iface struct {
	MTU       uint     `yaml:"mtu" description:"Interface MTU (Maximum Transmission Unit)" default:"1500"`
	XDPRTR    bool     `yaml:"xdprtr" description:"Should XDPRTR be loaded on this interface?" default:"false"`
	Addresses []string `yaml:"addresses" description:"List of addresses to add to this interface"`
	Dummy     bool     `yaml:"dummy" description:"Should a new dummy interface be created with this configuration?" default:"false"`
	Down      bool     `yaml:"down" description:"Should the interface be set to a down state?" default:"false"`
}

// loadConfig loads a configuration file from a YAML file
func loadConfig(configBlob []byte) (*config, error) {
	var c config
	if err := yaml.UnmarshalStrict(configBlob, &c); err != nil {
		return nil, errors.New("yaml unmarshal: " + err.Error())
	}

	validate := validator.New()
	if err := validate.Struct(&c); err != nil {
		return nil, errors.New("validation: " + err.Error())
	}

	// Check for invalid templates
	for templateName, templateData := range c.Templates {
		if templateData.Template != nil && *templateData.Template != "" {
			log.Fatalf("templates must not have a template attribute set, but %s does", templateName)
		}
	}

	// Assign values from template
	for peerName, peerData := range c.Peers { // For each peer
		if peerData.Template != nil && *peerData.Template != "" {
			template := c.Templates[*peerData.Template]
			if template == nil {
				log.Fatalf("template %s not found", *peerData.Template)
			}
			templateValue := reflect.ValueOf(*template)
			peerValue := reflect.ValueOf(c.Peers[peerName]).Elem()

			templateValueType := templateValue.Type()
			for i := 0; i < templateValueType.NumField(); i++ {
				fieldName := templateValueType.Field(i).Name
				peerFieldValue := peerValue.FieldByName(fieldName)
				if fieldName != "Template" { // Ignore the template attribute
					pVal := reflect.Indirect(peerFieldValue)
					peerHasValueConfigured := pVal.IsValid()
					tValue := templateValue.Field(i)
					templateHasValueConfigured := !tValue.IsNil()

					if peerHasValueConfigured {
						// Dont do anything
					} else if templateHasValueConfigured && !peerHasValueConfigured {
						// Use the templates value
						peerFieldValue.Set(templateValue.Field(i))
					}

					//log.Printf("[%s] %s val: %+v type: %T hasConfigured: %v", peerName, fieldName, tValue, tValue, templateHasValueConfigured)
				}
			}
		} // end peer template processor

		// Set default values
		peerValue := reflect.ValueOf(c.Peers[peerName]).Elem()
		templateValueType := peerValue.Type()
		for i := 0; i < templateValueType.NumField(); i++ {
			fieldName := templateValueType.Field(i).Name
			fieldValue := peerValue.FieldByName(fieldName)
			defaultString := templateValueType.Field(i).Tag.Get("default")
			if defaultString == "" {
				log.Fatalf("code error: field %s has no default value", fieldName)
			}
			//log.Printf("peer %s field %s value %+v", peerName, fieldName, fieldValue)
			if fieldValue.IsNil() {
				elemToSwitch := templateValueType.Field(i).Type.Elem().Kind()
				switch elemToSwitch {
				case reflect.String:
					fieldValue.Set(reflect.ValueOf(&defaultString))
				case reflect.Int:
					defaultValueInt, err := strconv.Atoi(defaultString)
					if err != nil {
						log.Fatalf("cant convert '%s' to uint", defaultString)
					}
					fieldValue.Set(reflect.ValueOf(&defaultValueInt))
				case reflect.Bool:
					var err error // explicit declaration used to avoid scope issues of defaultValue
					defaultBool, err := strconv.ParseBool(defaultString)
					if err != nil {
						log.Fatalf("can't parse bool %s", defaultString)
					}
					fieldValue.Set(reflect.ValueOf(&defaultBool))
				case reflect.Struct, reflect.Slice:
					// Ignore structs and slices
				default:
					log.Fatalf("unknown kind %+v for field %s", elemToSwitch, fieldName)
				}
			}
		}
	}

	// Parse origin routes by assembling OriginIPv{4,6} lists by address family
	for _, prefix := range c.Prefixes {
		pfx, _, err := net.ParseCIDR(prefix)
		if err != nil {
			return nil, errors.New("invalid origin prefix: " + prefix)
		}

		if pfx.To4() == nil { // If IPv6
			c.Prefixes4 = append(c.Prefixes4, prefix)
		} else { // If IPv4
			c.Prefixes6 = append(c.Prefixes6, prefix)
		}
	}

	// Initialize static maps
	c.Augments.Statics4 = map[string]string{}
	c.Augments.Statics6 = map[string]string{}

	// Parse static routes
	for prefix, nexthop := range c.Augments.Statics {
		pfx, _, err := net.ParseCIDR(prefix)
		if err != nil {
			return nil, errors.New("invalid static prefix: " + prefix)
		}
		if net.ParseIP(nexthop) == nil {
			return nil, errors.New("invalid static nexthop: " + nexthop)
		}

		if pfx.To4() == nil { // If IPv6
			c.Augments.Statics6[prefix] = nexthop
		} else { // If IPv4
			c.Augments.Statics4[prefix] = nexthop
		}
	}

	// Parse VRRP configs
	for _, vrrpInstance := range c.VRRPInstances {
		// Sort VIPs by address family
		for _, vip := range vrrpInstance.VIPs {
			ip, _, err := net.ParseCIDR(vip)
			if err != nil {
				return nil, errors.New("invalid VIP: " + vip)
			}

			if ip.To4() == nil { // If IPv6
				vrrpInstance.VIPs6 = append(vrrpInstance.VIPs6, vip)
			} else { // If IPv4
				vrrpInstance.VIPs4 = append(vrrpInstance.VIPs4, vip)
			}
		}

		// Validate vrrpInstance
		if vrrpInstance.State == "primary" {
			vrrpInstance.State = "MASTER"
		} else if vrrpInstance.State == "backup" {
			vrrpInstance.State = "BACKUP"
		} else {
			return nil, errors.New("VRRP state must be 'primary' or 'backup', unexpected " + vrrpInstance.State)
		}
	}

	for _, peerData := range c.Peers {
		// Build static prefix filters
		if peerData.Prefixes != nil {
			for _, prefix := range *peerData.Prefixes {
				pfx, _, err := net.ParseCIDR(prefix)
				if err != nil {
					return nil, errors.New("invalid prefix: " + prefix)
				}

				if pfx.To4() == nil { // If IPv6
					*peerData.PrefixSet4 = append(*peerData.PrefixSet4, prefix)
				} else { // If IPv4
					*peerData.PrefixSet6 = append(*peerData.PrefixSet6, prefix)
				}
			}
		}

		// Categorize communities
		if peerData.ImportCommunities != nil {
			for _, community := range *peerData.ImportCommunities {
				communityType := categorizeCommunity(community)
				if communityType == "standard" {
					*peerData.ImportStandardCommunities = append(*peerData.ImportStandardCommunities, community)
				} else if communityType == "large" {
					*peerData.ImportLargeCommunities = append(*peerData.ImportLargeCommunities, community)
				} else {
					return nil, errors.New("invalid import community: " + community)
				}
			}
		}

		if peerData.ExportCommunities != nil {
			for _, community := range *peerData.ExportCommunities {
				communityType := categorizeCommunity(community)
				if communityType == "standard" {
					*peerData.ExportStandardCommunities = append(*peerData.ExportStandardCommunities, community)
				} else if communityType == "large" {
					*peerData.ExportLargeCommunities = append(*peerData.ExportLargeCommunities, community)
				} else {
					return nil, errors.New("invalid export community: " + community)
				}
			}
		}
		if peerData.AnnounceCommunities != nil {
			for _, community := range *peerData.AnnounceCommunities {
				communityType := categorizeCommunity(community)

				if communityType == "standard" {
					*peerData.AnnounceStandardCommunities = append(*peerData.AnnounceStandardCommunities, community)
				} else if communityType == "large" {
					*peerData.AnnounceLargeCommunities = append(*peerData.AnnounceLargeCommunities, community)
				} else {
					return nil, errors.New("invalid announce community: " + community)
				}
			}
		}

		// Check for no originated prefixes but announce-originated enabled
		if len(c.Prefixes) < 1 && *peerData.AnnounceOriginated {
			// No locally originated prefixes are defined, so there's nothing to originate
			*peerData.AnnounceOriginated = false
		}
	} // end peer loop

	return &c, nil // nil error
}

func documentConfigTypes(t reflect.Type) {
	fmt.Println("<!-- Code generated DO NOT EDIT -->")
	fmt.Println("# Configuration")
	var childTypes []reflect.Type
	fmt.Println("## " + strings.Replace(t.String(), "main.", "", -1))
	fmt.Println("| Option | Type | Default | Validation | Description |")
	fmt.Println("|--------|------|---------|------------|-------------|")
	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		description := field.Tag.Get("description")
		key := field.Tag.Get("yaml")
		validation := field.Tag.Get("validate")
		fDefault := field.Tag.Get("default")
		if fDefault != "" {
			fDefault = "`" + fDefault + "`"
		}

		if description == "" {
			log.Fatalf("code error: %s doesn't have a description", field.Name)
		} else if description != "-" { // Ignore descriptions that are -
			if strings.Contains(field.Type.String(), "main.") { // If the type is a config struct
				if field.Type.Kind() == reflect.Map || field.Type.Kind() == reflect.Slice { // Extract the element if the type is a map or slice
					childTypes = append(childTypes, field.Type.Elem())
				} else {
					childTypes = append(childTypes, field.Type)
				}
			}
			fmt.Printf("| `%s` | `%s` | %s | %s | %s |\n", key, strings.Replace(field.Type.String(), "main.", "", -1), fDefault, validation, description)
		}
	}
	fmt.Println()
	for _, childType := range childTypes {
		documentConfigTypes(childType)
	}
}

func documentConfig() {
	documentConfigTypes(reflect.TypeOf(config{}))
}

func documentCliFlags() {
	fmt.Println("<!-- Code generated DO NOT EDIT -->")
	fmt.Println("## CLI Flags")
	fmt.Println("| Option | Type | Description |")
	fmt.Println("|--------|------|-------------|")
	t := reflect.TypeOf(cliFlags)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		short := field.Tag.Get("short")
		long := field.Tag.Get("long")
		description := field.Tag.Get("description")

		fmt.Printf("| `-%s`, `--%s` | `%s` | %s |\n", short, long, field.Type.String(), description)
	}
}
