// This is a generated file. Do not edit directly.

module k8s.io/kube-scheduler

go 1.16

require (
	github.com/google/go-cmp v0.5.4
	k8s.io/api v0.21.0-rc.0
	k8s.io/apimachinery v0.21.0-rc.0
	k8s.io/component-base v0.21.0-rc.0
	sigs.k8s.io/yaml v1.2.0
)

replace (
	github.com/imdario/mergo => github.com/imdario/mergo v0.3.5
	github.com/onsi/ginkgo => github.com/openshift/ginkgo v4.7.0-origin.0+incompatible
	go.uber.org/multierr => go.uber.org/multierr v1.1.0
	k8s.io/api => ../api
	k8s.io/apimachinery => ../apimachinery
	k8s.io/client-go => ../client-go
	k8s.io/component-base => ../component-base
	k8s.io/kube-scheduler => ../kube-scheduler
)
