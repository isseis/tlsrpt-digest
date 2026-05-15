package notify_test

import (
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
)

func TestPolicyType_Constants(t *testing.T) {
	assert.Equal(t, notify.PolicyType("sts"), notify.PolicyTypeSTS)
	assert.Equal(t, notify.PolicyType("tlsa"), notify.PolicyTypeTLSA)
	assert.Equal(t, notify.PolicyType("no-policy-found"), notify.PolicyTypeNoPolicyFound)
	assert.Equal(t, notify.PolicyType(""), notify.PolicyTypeUnknown)
}
