/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package source

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"

	"sigs.k8s.io/external-dns/endpoint"
)

// Validates that ingressSource is a Source
var _ Source = &ingressSource{}

type IngressSuite struct {
	suite.Suite
	sc             Source
	fooWithTargets *networkv1.Ingress
}

func (suite *IngressSuite) SetupTest() {
	fakeClient := fake.NewClientset()

	suite.fooWithTargets = (fakeIngress{
		name:      "foo-with-targets",
		namespace: "default",
		dnsnames:  []string{"foo"},
		ips:       []string{"8.8.8.8", "2606:4700:4700::1111"},
		hostnames: []string{"v1"},
	}).Ingress()
	_, err := fakeClient.NetworkingV1().Ingresses(suite.fooWithTargets.Namespace).Create(context.Background(), suite.fooWithTargets, metav1.CreateOptions{})
	suite.NoError(err, "should succeed")

	suite.sc, err = NewIngressSource(
		context.TODO(),
		fakeClient,
		"",
		"",
		"{{.Name}}",
		false,
		false,
		false,
		false,
		labels.Everything(),
		[]string{},
	)
	suite.NoError(err, "should initialize ingress source")
}

func (suite *IngressSuite) TestResourceLabelIsSet() {
	endpoints, _ := suite.sc.Endpoints(context.Background())
	for _, ep := range endpoints {
		suite.Equal("ingress/default/foo-with-targets", ep.Labels[endpoint.ResourceLabelKey], "should set correct resource label")
	}
}

func TestIngress(t *testing.T) {
	t.Parallel()

	suite.Run(t, new(IngressSuite))
	t.Run("endpointsFromIngress", testEndpointsFromIngress)
	t.Run("endpointsFromIngressHostnameSourceAnnotation", testEndpointsFromIngressHostnameSourceAnnotation)
	t.Run("Endpoints", testIngressEndpoints)
}

func TestNewIngressSource(t *testing.T) {
	t.Parallel()

	for _, ti := range []struct {
		title                    string
		annotationFilter         string
		fqdnTemplate             string
		combineFQDNAndAnnotation bool
		expectError              bool
		ingressClassNames        []string
	}{
		{
			title:            "non-empty annotation filter label",
			expectError:      false,
			annotationFilter: "kubernetes.io/ingress.class=nginx",
		},
		{
			title:             "non-empty ingress class name list",
			expectError:       false,
			ingressClassNames: []string{"internal", "external"},
		},
		{
			title:             "ingress class name and annotation filter jointly specified",
			expectError:       true,
			ingressClassNames: []string{"internal", "external"},
			annotationFilter:  "kubernetes.io/ingress.class=nginx",
		},
	} {

		t.Run(ti.title, func(t *testing.T) {
			t.Parallel()

			_, err := NewIngressSource(
				t.Context(),
				fake.NewClientset(),
				"",
				ti.annotationFilter,
				ti.fqdnTemplate,
				ti.combineFQDNAndAnnotation,
				false,
				false,
				false,
				labels.Everything(),
				ti.ingressClassNames,
			)
			if ti.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func testEndpointsFromIngress(t *testing.T) {
	t.Parallel()

	for _, ti := range []struct {
		title                    string
		ingress                  fakeIngress
		ignoreHostnameAnnotation bool
		ignoreIngressTLSSpec     bool
		ignoreIngressRulesSpec   bool
		expected                 []*endpoint.Endpoint
	}{
		{
			title: "one rule.host one lb.hostname",
			ingress: fakeIngress{
				dnsnames:  []string{"foo.bar"}, // Kubernetes requires removal of trailing dot
				hostnames: []string{"lb.com"},  // Kubernetes omits the trailing dot
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title: "one rule.host one lb.IP",
			ingress: fakeIngress{
				dnsnames: []string{"foo.bar"},
				ips:      []string{"8.8.8.8"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			title: "one rule.host one lb.IPv6",
			ingress: fakeIngress{
				dnsnames: []string{"foo.bar"},
				ips:      []string{"2606:4700:4700::1111"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeAAAA,
					Targets:    endpoint.Targets{"2606:4700:4700::1111"},
				},
			},
		},
		{
			title: "one rule.host two lb.IP, two lb.IPv6 and two lb.Hostname",
			ingress: fakeIngress{
				dnsnames:  []string{"foo.bar"},
				ips:       []string{"8.8.8.8", "127.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"},
				hostnames: []string{"elb.com", "alb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8", "127.0.0.1"},
				},
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeAAAA,
					Targets:    endpoint.Targets{"2606:4700:4700::1111", "2606:4700:4700::1001"},
				},
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"elb.com", "alb.com"},
				},
			},
		},
		{
			title: "no rule.host",
			ingress: fakeIngress{
				ips:       []string{"8.8.8.8", "127.0.0.1"},
				hostnames: []string{"elb.com", "alb.com"},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title: "one empty rule.host",
			ingress: fakeIngress{
				dnsnames:  []string{""},
				ips:       []string{"8.8.8.8", "127.0.0.1"},
				hostnames: []string{"elb.com", "alb.com"},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title: "no targets",
			ingress: fakeIngress{
				dnsnames: []string{""},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title: "ignore rules with one rule.host one lb.hostname",
			ingress: fakeIngress{
				dnsnames:  []string{"test"},   // Kubernetes requires removal of trailing dot
				hostnames: []string{"lb.com"}, // Kubernetes omits the trailing dot
			},
			expected:               []*endpoint.Endpoint{},
			ignoreIngressRulesSpec: true,
		},
		{
			title: "invalid hostname does not generate endpoints",
			ingress: fakeIngress{
				dnsnames: []string{"this-is-an-exceedingly-long-label-that-external-dns-should-reject.example.org"},
			},
			expected: []*endpoint.Endpoint{},
		},
	} {
		t.Run(ti.title, func(t *testing.T) {
			realIngress := ti.ingress.Ingress()
			validateEndpoints(t, endpointsFromIngress(realIngress, ti.ignoreHostnameAnnotation, ti.ignoreIngressTLSSpec, ti.ignoreIngressRulesSpec), ti.expected)
		})
	}
}

func testEndpointsFromIngressHostnameSourceAnnotation(t *testing.T) {
	// Host names and host name annotation provided, with various values of the ingress-hostname-source annotation
	for _, ti := range []struct {
		title    string
		ingress  fakeIngress
		expected []*endpoint.Endpoint
	}{
		{
			title: "No ingress-hostname-source annotation, one rule.host, one annotation host",
			ingress: fakeIngress{
				dnsnames:    []string{"foo.bar"},
				annotations: map[string]string{hostnameAnnotationKey: "foo.baz"},
				hostnames:   []string{"lb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
				{
					DNSName:    "foo.baz",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title: "No ingress-hostname-source annotation, one rule.host",
			ingress: fakeIngress{
				dnsnames:  []string{"foo.bar"},
				hostnames: []string{"lb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title: "No ingress-hostname-source annotation, one rule.host, one annotation host",
			ingress: fakeIngress{
				dnsnames:    []string{"foo.bar"},
				annotations: map[string]string{hostnameAnnotationKey: "foo.baz"},
				hostnames:   []string{"lb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
				{
					DNSName:    "foo.baz",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title: "Ingress-hostname-source=defined-hosts-only, one rule.host, one annotation host",
			ingress: fakeIngress{
				dnsnames:    []string{"foo.bar"},
				annotations: map[string]string{hostnameAnnotationKey: "foo.baz", ingressHostnameSourceKey: "defined-hosts-only"},
				hostnames:   []string{"lb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.bar",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title: "Ingress-hostname-source=annotation-only, one rule.host, one annotation host",
			ingress: fakeIngress{
				dnsnames:    []string{"foo.bar"},
				annotations: map[string]string{hostnameAnnotationKey: "foo.baz", ingressHostnameSourceKey: "annotation-only"},
				hostnames:   []string{"lb.com"},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "foo.baz",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
	} {
		t.Run(ti.title, func(t *testing.T) {
			realIngress := ti.ingress.Ingress()
			validateEndpoints(t, endpointsFromIngress(realIngress, false, false, false), ti.expected)
		})
	}
}

func testIngressEndpoints(t *testing.T) {
	t.Parallel()

	namespace := "testing"
	for _, ti := range []struct {
		title                    string
		targetNamespace          string
		annotationFilter         string
		ingressItems             []fakeIngress
		expected                 []*endpoint.Endpoint
		expectError              bool
		fqdnTemplate             string
		combineFQDNAndAnnotation bool
		ignoreHostnameAnnotation bool
		ignoreIngressTLSSpec     bool
		ignoreIngressRulesSpec   bool
		ingressLabelSelector     labels.Selector
		ingressClassNames        []string
	}{
		{
			title:           "no ingress",
			targetNamespace: "",
		},
		{
			title:           "two simple ingresses",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: namespace,
					dnsnames:  []string{"new.org"},
					hostnames: []string{"lb.com"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
				{
					DNSName:    "new.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title:           "ipv6 ingress",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"2001:DB8::1"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeAAAA,
					Targets:    endpoint.Targets{"2001:DB8::1"},
				},
			},
		},
		{
			title:           "dualstack ingress",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8", "2001:DB8::1"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeAAAA,
					Targets:    endpoint.Targets{"2001:DB8::1"},
				},
			},
		},
		{
			title:                  "ignore rules",
			targetNamespace:        "",
			ignoreIngressRulesSpec: true,
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: namespace,
					dnsnames:  []string{"new.org"},
					hostnames: []string{"lb.com"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title:           "two simple ingresses on different namespaces",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: "testing1",
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: "testing2",
					dnsnames:  []string{"new.org"},
					hostnames: []string{"lb.com"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
				{
					DNSName:    "new.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title:           "two simple ingresses on different namespaces with target namespace",
			targetNamespace: "testing1",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: "testing1",
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: "testing2",
					dnsnames:  []string{"new.org"},
					hostnames: []string{"lb.com"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			title:            "valid matching annotation filter expression",
			targetNamespace:  "",
			annotationFilter: "kubernetes.io/ingress.class in (alb, nginx)",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "nginx",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			title:            "valid non-matching annotation filter expression",
			targetNamespace:  "",
			annotationFilter: "kubernetes.io/ingress.class in (alb, nginx)",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "tectonic",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title:            "invalid annotation filter expression",
			targetNamespace:  "",
			annotationFilter: "kubernetes.io/ingress.name in (a b)",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "alb",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected:    []*endpoint.Endpoint{},
			expectError: true,
		},
		{
			title:            "valid matching annotation filter label",
			targetNamespace:  "",
			annotationFilter: "kubernetes.io/ingress.class=nginx",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "nginx",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			title:            "valid non-matching annotation filter label",
			targetNamespace:  "",
			annotationFilter: "kubernetes.io/ingress.class=nginx",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "alb",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title:           "our controller type is dns-controller",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						controllerAnnotationKey: controllerAnnotationValue,
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			title:           "different controller types are ignored",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						controllerAnnotationKey: "some-other-tool",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title:           "template for ingress if host is missing",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						controllerAnnotationKey: controllerAnnotationValue,
					},
					dnsnames:  []string{},
					ips:       []string{"8.8.8.8"},
					hostnames: []string{"elb.com"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "fake1.ext-dns.test.com",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
				{
					DNSName:    "fake1.ext-dns.test.com",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"elb.com"},
				},
			},
			fqdnTemplate: "{{.Name}}.ext-dns.test.com",
		},
		{
			title:           "another controller annotation skipped even with template",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						controllerAnnotationKey: "other-controller",
					},
					dnsnames: []string{},
					ips:      []string{"8.8.8.8"},
				},
			},
			expected:     []*endpoint.Endpoint{},
			fqdnTemplate: "{{.Name}}.ext-dns.test.com",
		},
		{
			title:           "multiple FQDN template hostnames",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					annotations: map[string]string{},
					dnsnames:    []string{},
					ips:         []string{"8.8.8.8"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "fake1.ext-dns.test.com",
					Targets:    endpoint.Targets{"8.8.8.8"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "fake1.ext-dna.test.com",
					Targets:    endpoint.Targets{"8.8.8.8"},
					RecordType: endpoint.RecordTypeA,
				},
			},
			fqdnTemplate: "{{.Name}}.ext-dns.test.com, {{.Name}}.ext-dna.test.com",
		},
		{
			title:           "multiple FQDN template hostnames",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					annotations: map[string]string{},
					dnsnames:    []string{},
					ips:         []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "fake1.ext-dns.test.com",
					Targets:    endpoint.Targets{"8.8.8.8"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "fake1.ext-dna.test.com",
					Targets:    endpoint.Targets{"8.8.8.8"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "fake2.ext-dns.test.com",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "fake2.ext-dna.test.com",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
			},
			fqdnTemplate:             "{{.Name}}.ext-dns.test.com, {{.Name}}.ext-dna.test.com",
			combineFQDNAndAnnotation: true,
		},
		{
			title:           "ingress rules with annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
				{
					name:      "fake2",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
					},
					dnsnames: []string{"example2.org"},
					ips:      []string{"8.8.8.8"},
				},
				{
					name:      "fake3",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "1.2.3.4",
					},
					dnsnames: []string{"example3.org"},
					ips:      []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "example2.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "example3.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with single tls having single hostname",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"example.org"}},
					ips:         []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with single tls having multiple hostnames",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"example.org", "example2.org"}},
					ips:         []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "example2.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with multiple tls having multiple hostnames",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"example.org", "example2.org"}, {"example3.org", "example4.org"}},
					ips:         []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "example2.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "example3.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "example4.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with hostname annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						hostnameAnnotationKey: "dns-through-hostname.com",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "dns-through-hostname.com",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with hostname annotation having multiple hostnames",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						hostnameAnnotationKey: "dns-through-hostname.com, another-dns-through-hostname.com",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "dns-through-hostname.com",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
				{
					DNSName:    "another-dns-through-hostname.com",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
		},
		{
			title:           "ingress rules with hostname and target annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						hostnameAnnotationKey: "dns-through-hostname.com",
						targetAnnotationKey:   "ingress-target.com",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "dns-through-hostname.com",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
			},
		},
		{
			title:           "ingress rules with annotation and custom TTL",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
						ttlAnnotationKey:    "6",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
				{
					name:      "fake2",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
						ttlAnnotationKey:    "1",
					},
					dnsnames: []string{"example2.org"},
					ips:      []string{"8.8.8.8"},
				},
				{
					name:      "fake3",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
						ttlAnnotationKey:    "10s",
					},
					dnsnames: []string{"example3.org"},
					ips:      []string{"8.8.4.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordTTL:  endpoint.TTL(6),
				},
				{
					DNSName:    "example2.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordTTL:  endpoint.TTL(1),
				},
				{
					DNSName:    "example3.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordTTL:  endpoint.TTL(10),
				},
			},
		},
		{
			title:           "ingress rules with alias and target annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
						aliasAnnotationKey:  "true",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
					ProviderSpecific: endpoint.ProviderSpecific{{
						Name: "alias", Value: "true",
					}},
				},
			},
		},
		{
			title:           "ingress rules with alias set false and target annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
						aliasAnnotationKey:  "false",
					},
					dnsnames: []string{"example.org"},
					ips:      []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
			},
		},
		{
			title:           "template for ingress with annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
					},
					dnsnames:  []string{},
					ips:       []string{},
					hostnames: []string{},
				},
				{
					name:      "fake2",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "ingress-target.com",
					},
					dnsnames: []string{},
					ips:      []string{"8.8.8.8"},
				},
				{
					name:      "fake3",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "1.2.3.4",
					},
					dnsnames:  []string{},
					ips:       []string{},
					hostnames: []string{},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "fake1.ext-dns.test.com",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "fake2.ext-dns.test.com",
					Targets:    endpoint.Targets{"ingress-target.com"},
					RecordType: endpoint.RecordTypeCNAME,
				},
				{
					DNSName:    "fake3.ext-dns.test.com",
					Targets:    endpoint.Targets{"1.2.3.4"},
					RecordType: endpoint.RecordTypeA,
				},
			},
			fqdnTemplate: "{{.Name}}.ext-dns.test.com",
		},
		{
			title:           "Ingress with empty annotation",
			targetNamespace: "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					annotations: map[string]string{
						targetAnnotationKey: "",
					},
					dnsnames:  []string{},
					ips:       []string{},
					hostnames: []string{},
				},
			},
			expected:     []*endpoint.Endpoint{},
			fqdnTemplate: "{{.Name}}.ext-dns.test.com",
		},
		{
			title:                    "ignore hostname annotation",
			targetNamespace:          "",
			ignoreHostnameAnnotation: true,
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
				},
				{
					name:      "fake2",
					namespace: namespace,
					annotations: map[string]string{
						hostnameAnnotationKey: "dns-through-hostname.com",
					},
					dnsnames:  []string{"new.org"},
					hostnames: []string{"lb.com"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
				{
					DNSName:    "new.org",
					RecordType: endpoint.RecordTypeCNAME,
					Targets:    endpoint.Targets{"lb.com"},
				},
			},
		},
		{
			title:                "ignore tls section",
			targetNamespace:      "",
			ignoreIngressTLSSpec: true,
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"example.org"}},
					ips:         []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
		{
			title:                "reading tls section",
			targetNamespace:      "",
			ignoreIngressTLSSpec: false,
			ingressItems: []fakeIngress{
				{
					name:        "fake1",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"example.org"}},
					ips:         []string{"1.2.3.4"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"1.2.3.4"},
				},
			},
		},
		{
			title:             "ingressClassName filtering",
			targetNamespace:   "",
			ingressClassNames: []string{"public", "dmz"},
			ingressItems: []fakeIngress{
				{
					name:        "none",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"none.example.org"}},
					ips:         []string{"1.0.0.0"},
				},
				{
					name:             "fake-public",
					namespace:        namespace,
					tlsdnsnames:      [][]string{{"example.org"}},
					ips:              []string{"1.2.3.4"},
					ingressClassName: "public", // match
				},
				{
					name:             "fake-internal",
					namespace:        namespace,
					tlsdnsnames:      [][]string{{"int.example.org"}},
					ips:              []string{"2.3.4.5"},
					ingressClassName: "internal",
				},
				{
					name:             "fake-dmz",
					namespace:        namespace,
					tlsdnsnames:      [][]string{{"dmz.example.org"}},
					ips:              []string{"3.4.5.6"},
					ingressClassName: "dmz", // match
				},
				{
					name:        "annotated-dmz",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"annodmz.example.org"}},
					ips:         []string{"4.5.6.7"},
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "dmz", // match
					},
				},
				{
					name:        "fake-internal-annotated-dmz",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"int-annodmz.example.org"}},
					ips:         []string{"5.6.7.8"},
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "dmz", // match but ignored (non-empty ingressClassName)
					},
					ingressClassName: "internal",
				},
				{
					name:        "fake-dmz-annotated-internal",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"dmz-annoint.example.org"}},
					ips:         []string{"6.7.8.9"},
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "internal",
					},
					ingressClassName: "dmz", // match
				},
				{
					name:        "empty-annotated-dmz",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"empty-annotdmz.example.org"}},
					ips:         []string{"7.8.9.0"},
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "dmz", // match (empty ingressClassName)
					},
					ingressClassName: "",
				},
				{
					name:        "empty-annotated-internal",
					namespace:   namespace,
					tlsdnsnames: [][]string{{"empty-annotint.example.org"}},
					ips:         []string{"8.9.0.1"},
					annotations: map[string]string{
						"kubernetes.io/ingress.class": "internal",
					},
					ingressClassName: "",
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"1.2.3.4"},
				},
				{
					DNSName:    "dmz.example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"3.4.5.6"},
				},
				{
					DNSName:    "annodmz.example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"4.5.6.7"},
				},
				{
					DNSName:    "dmz-annoint.example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"6.7.8.9"},
				},
				{
					DNSName:    "empty-annotdmz.example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"7.8.9.0"},
				},
			},
		},
		{
			ingressLabelSelector: labels.SelectorFromSet(labels.Set{"app": "web-external"}),
			title:                "ingress with matching labels",
			targetNamespace:      "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
					labels:    map[string]string{"app": "web-external", "name": "reverse-proxy"},
				},
			},
			expected: []*endpoint.Endpoint{
				{
					DNSName:    "example.org",
					RecordType: endpoint.RecordTypeA,
					Targets:    endpoint.Targets{"8.8.8.8"},
				},
			},
		},
		{
			ingressLabelSelector: labels.SelectorFromSet(labels.Set{"app": "web-external"}),
			title:                "ingress without matching labels",
			targetNamespace:      "",
			ingressItems: []fakeIngress{
				{
					name:      "fake1",
					namespace: namespace,
					dnsnames:  []string{"example.org"},
					ips:       []string{"8.8.8.8"},
					labels:    map[string]string{"app": "web-internal", "name": "reverse-proxy"},
				},
			},
			expected: []*endpoint.Endpoint{},
		},
	} {

		t.Run(ti.title, func(t *testing.T) {
			t.Parallel()

			fakeClient := fake.NewClientset()
			for _, item := range ti.ingressItems {
				ingress := item.Ingress()
				_, err := fakeClient.NetworkingV1().Ingresses(ingress.Namespace).Create(t.Context(), ingress, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			if ti.ingressLabelSelector == nil {
				ti.ingressLabelSelector = labels.Everything()
			}

			source, _ := NewIngressSource(
				context.TODO(),
				fakeClient,
				ti.targetNamespace,
				ti.annotationFilter,
				ti.fqdnTemplate,
				ti.combineFQDNAndAnnotation,
				ti.ignoreHostnameAnnotation,
				ti.ignoreIngressTLSSpec,
				ti.ignoreIngressRulesSpec,
				ti.ingressLabelSelector,
				ti.ingressClassNames,
			)
			// Informer cache has all of the ingresses. Retrieve and validate their endpoints.
			res, err := source.Endpoints(t.Context())
			if ti.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			validateEndpoints(t, res, ti.expected)

			// TODO; when all resources have the resource label, we could add this check to the validateEndpoints function.
			for _, ep := range res {
				require.Contains(t, ep.Labels, endpoint.ResourceLabelKey)
			}
		})
	}
}

// ingress specific helper functions
type fakeIngress struct {
	dnsnames         []string
	tlsdnsnames      [][]string
	ips              []string
	hostnames        []string
	namespace        string
	name             string
	annotations      map[string]string
	labels           map[string]string
	ingressClassName string
}

func (ing fakeIngress) Ingress() *networkv1.Ingress {
	ingress := &networkv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ing.namespace,
			Name:        ing.name,
			Annotations: ing.annotations,
			Labels:      ing.labels,
		},
		Spec: networkv1.IngressSpec{
			Rules:            []networkv1.IngressRule{},
			IngressClassName: &ing.ingressClassName,
		},
		Status: networkv1.IngressStatus{
			LoadBalancer: networkv1.IngressLoadBalancerStatus{
				Ingress: []networkv1.IngressLoadBalancerIngress{},
			},
		},
	}
	for _, dnsname := range ing.dnsnames {
		ingress.Spec.Rules = append(ingress.Spec.Rules, networkv1.IngressRule{
			Host: dnsname,
		})
	}
	for _, hosts := range ing.tlsdnsnames {
		ingress.Spec.TLS = append(ingress.Spec.TLS, networkv1.IngressTLS{
			Hosts: hosts,
		})
	}
	for _, ip := range ing.ips {
		ingress.Status.LoadBalancer.Ingress = append(ingress.Status.LoadBalancer.Ingress, networkv1.IngressLoadBalancerIngress{
			IP: ip,
		})
	}
	for _, hostname := range ing.hostnames {
		ingress.Status.LoadBalancer.Ingress = append(ingress.Status.LoadBalancer.Ingress, networkv1.IngressLoadBalancerIngress{
			Hostname: hostname,
		})
	}
	return ingress
}
