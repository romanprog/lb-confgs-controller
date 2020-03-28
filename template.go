package main

import (
	"log"
	"os"
	"text/template"
)

//type projectsMetadataType map[string]*projectMetadata

// Generate virtual hosts config from consul metadata.
// Use consul-template binary (shell command).
func genConfig(projectsMetadata projectsMetadataType) (Error error) {
	log.Println(*projectsMetadata["c9b8b104b9e54599add63fcfb652d810"].Domains["test-ssl-acme.horoshop.ua"])
	return nil
}

func testTmpl() {
	domains := make(map[string]*projectDomainData)
	domains["example1.com"] = &projectDomainData{false, 0}
	domains["example2.com"] = &projectDomainData{true, 1}
	domains2 := make(map[string]*projectDomainData)
	domains2["example21.com"] = &projectDomainData{false, 0}
	domains2["example22.com"] = &projectDomainData{false, 0}

	projects := make(projectsMetadataType)
	projects["UUID111"] = &projectMetadata{domains, "nfs1", "2.4.4", "radis::cache", "redis::sessions", "DbMasterUrl", "DbSlaveUrl", "LogUrl", "core_path", "0", "front", "root"}
	projects["UUID111"].Domains = domains
	projects["UUID222"] = &projectMetadata{domains2, "nfs2", "2.4.4", "radis::cache", "redis::sessions", "DbMasterUrl", "DbSlaveUrl", "LogUrl", "core_path", "0", "front", "root"}

	var tpl string = `{{ range $key, $value := . }}
	UUID {{ $key }} {{ .Storage }}
	{{ $storage := .Storage -}}
	server_name {{ range $key, $_ := .Domains -}}{{ $key }} {{ $key }}. {{ end -}}
{{ end }}
`
	tmpl, err := template.New("test").Parse(tpl)
	if err != nil {
		log.Fatal(err)
	}
	err = tmpl.Execute(os.Stdout, projects)
	if err != nil {
		log.Fatal(err)
	}
}
