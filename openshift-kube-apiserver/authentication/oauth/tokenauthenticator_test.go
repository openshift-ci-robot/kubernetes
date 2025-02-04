package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	clienttesting "k8s.io/client-go/testing"

	oauthv1 "github.com/openshift/api/oauth/v1"
	userv1 "github.com/openshift/api/user/v1"
	oauthfake "github.com/openshift/client-go/oauth/clientset/versioned/fake"
	oauthclient "github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	userfake "github.com/openshift/client-go/user/clientset/versioned/fake"
)

func TestAuthenticateTokenInvalidUID(t *testing.T) {
	token, tokenHash := generateOAuthTokenPair()
	fakeOAuthClient := oauthfake.NewSimpleClientset(
		&oauthv1.OAuthAccessToken{
			ObjectMeta: metav1.ObjectMeta{Name: tokenHash, CreationTimestamp: metav1.Time{Time: time.Now()}},
			ExpiresIn:  600, // 10 minutes
			UserName:   "foo",
			UserUID:    string("bar1"),
		},
	)
	fakeUserClient := userfake.NewSimpleClientset(&userv1.User{ObjectMeta: metav1.ObjectMeta{Name: "foo", UID: "bar2"}})

	tokenAuthenticator := NewTokenAuthenticator(fakeOAuthClient.OauthV1().OAuthAccessTokens(), fakeUserClient.UserV1().Users(), NoopGroupMapper{}, nil, NewUIDValidator())

	userInfo, found, err := tokenAuthenticator.AuthenticateToken(context.TODO(), token)
	if found {
		t.Error("Found token, but it should be missing!")
	}
	if err.Error() != "user.UID (bar2) does not match token.userUID (bar1)" {
		t.Errorf("Unexpected error: %v", err)
	}
	if userInfo != nil {
		t.Errorf("Unexpected user: %v", userInfo)
	}
}

func TestAuthenticateTokenFormats(t *testing.T) {
	h := sha256.Sum256([]byte("token"))
	tokenSha256 := base64.RawURLEncoding.EncodeToString(h[:])

	h2 := sha256.Sum256([]byte("token2"))
	token2Sha256 := base64.RawURLEncoding.EncodeToString(h2[:])

	fakeOAuthClient := oauthfake.NewSimpleClientset(
		&oauthv1.OAuthAccessToken{
			ObjectMeta: metav1.ObjectMeta{Name: "sha256~" + tokenSha256, CreationTimestamp: metav1.Time{Time: time.Now()}},
			ExpiresIn:  600, // 10 minutes
			UserName:   "tokenUser",
			UserUID:    "tokenUserID",
		},
		&oauthv1.OAuthAccessToken{
			ObjectMeta: metav1.ObjectMeta{Name: "token2", CreationTimestamp: metav1.Time{Time: time.Now()}},
			ExpiresIn:  600, // 10 minutes
			UserName:   "token2User",
			UserUID:    "token2UserID",
		},
	)
	fakeUserClient := userfake.NewSimpleClientset(
		&userv1.User{ObjectMeta: metav1.ObjectMeta{Name: "tokenUser", UID: "tokenUserID"}},
		&userv1.User{ObjectMeta: metav1.ObjectMeta{Name: "token2User", UID: "token2UserID"}},
	)

	tokenAuthenticator := NewTokenAuthenticator(fakeOAuthClient.OauthV1().OAuthAccessTokens(), fakeUserClient.UserV1().Users(), NoopGroupMapper{}, nil, NewUIDValidator())

	type Test struct {
		name             string
		bearerToken      string
		expectedError    bool
		expectedFound    bool
		expectedUserName string
	}
	for _, test := range []Test{
		{"unknown", "unknown", true, false, ""},
		{"unprefixed token", "token", true, false, ""},
		{"prefixed token", "sha256~token", false, true, "tokenUser"},
		{"unprefixed hash token", tokenSha256, true, false, ""},
		{"prefixed hash token", "sha256~" + tokenSha256, true, false, ""},
		{"unprefixed token2", "token2", true, false, ""},
		{"prefixed token2", "sha256~token2", true, false, ""},
		{"unprefixed hash token2", token2Sha256, true, false, ""},
		{"prefixed hash token2", "sha256~" + token2Sha256, true, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			userInfo, found, err := tokenAuthenticator.AuthenticateToken(context.TODO(), test.bearerToken)
			if !test.expectedError && err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if test.expectedError && err == nil {
				t.Fatalf("Unexpected non-error: found=%v userInfo=%#v", found, userInfo)
			}
			if !test.expectedFound && found {
				t.Fatalf("Expected error, but token was found: %#v", userInfo)
			}
			if test.expectedFound && !found {
				t.Fatalf("Expected token to be found, but it was not")
			}
			if userInfo != nil && userInfo.User.GetName() != test.expectedUserName {
				t.Errorf("Wrong user name, expected %q, got %q", test.expectedUserName, userInfo.User.GetName())
			}
			if userInfo == nil && len(test.expectedUserName) > 0 {
				t.Errorf("Got no user info, but expected user name %q", test.expectedUserName)
			}
		})
	}
}

func TestAuthenticateTokenNotFoundSuppressed(t *testing.T) {
	fakeOAuthClient := oauthfake.NewSimpleClientset()
	fakeUserClient := userfake.NewSimpleClientset()
	tokenAuthenticator := NewTokenAuthenticator(fakeOAuthClient.OauthV1().OAuthAccessTokens(), fakeUserClient.UserV1().Users(), NoopGroupMapper{}, nil)

	userInfo, found, err := tokenAuthenticator.AuthenticateToken(context.TODO(), "sha256~token")
	if found {
		t.Error("Found token, but it should be missing!")
	}
	if err != errLookup {
		t.Error("Expected not found error to be suppressed with lookup error")
	}
	if userInfo != nil {
		t.Errorf("Unexpected user: %v", userInfo)
	}
}

func TestAuthenticateTokenOtherGetErrorSuppressed(t *testing.T) {
	fakeOAuthClient := oauthfake.NewSimpleClientset()
	fakeOAuthClient.PrependReactor("get", "oauthaccesstokens", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, errors.New("get error")
	})
	fakeUserClient := userfake.NewSimpleClientset()
	tokenAuthenticator := NewTokenAuthenticator(fakeOAuthClient.OauthV1().OAuthAccessTokens(), fakeUserClient.UserV1().Users(), NoopGroupMapper{}, nil)

	userInfo, found, err := tokenAuthenticator.AuthenticateToken(context.TODO(), "sha256~token")
	if found {
		t.Error("Found token, but it should be missing!")
	}
	if err != errLookup {
		t.Error("Expected custom get error to be suppressed with lookup error")
	}
	if userInfo != nil {
		t.Errorf("Unexpected user: %v", userInfo)
	}
}

func TestAuthenticateTokenTimeout(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	testClock := clock.NewFakeClock(time.Time{})

	defaultTimeout := int32(30) // 30 seconds
	clientTimeout := int32(15)  // 15 seconds
	minTimeout := int32(10)     // 10 seconds -> 10/3 = a tick per 3 seconds

	testClient := oauthv1.OAuthClient{
		ObjectMeta:                          metav1.ObjectMeta{Name: "testClient"},
		AccessTokenInactivityTimeoutSeconds: &clientTimeout,
	}
	quickClient := oauthv1.OAuthClient{
		ObjectMeta:                          metav1.ObjectMeta{Name: "quickClient"},
		AccessTokenInactivityTimeoutSeconds: &minTimeout,
	}
	slowClient := oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{Name: "slowClient"},
	}
	testTokenClear, testTokenHash := generateOAuthTokenPair()
	testToken := oauthv1.OAuthAccessToken{
		ObjectMeta:               metav1.ObjectMeta{Name: testTokenHash, CreationTimestamp: metav1.Time{Time: testClock.Now()}},
		ClientName:               "testClient",
		ExpiresIn:                600, // 10 minutes
		UserName:                 "foo",
		UserUID:                  string("bar"),
		InactivityTimeoutSeconds: clientTimeout,
	}

	quickTokenClear, quickTokenHash := generateOAuthTokenPair()
	quickToken := oauthv1.OAuthAccessToken{
		ObjectMeta:               metav1.ObjectMeta{Name: quickTokenHash, CreationTimestamp: metav1.Time{Time: testClock.Now()}},
		ClientName:               "quickClient",
		ExpiresIn:                600, // 10 minutes
		UserName:                 "foo",
		UserUID:                  string("bar"),
		InactivityTimeoutSeconds: minTimeout,
	}
	slowTokenClear, slowTokenHash := generateOAuthTokenPair()
	slowToken := oauthv1.OAuthAccessToken{
		ObjectMeta:               metav1.ObjectMeta{Name: slowTokenHash, CreationTimestamp: metav1.Time{Time: testClock.Now()}},
		ClientName:               "slowClient",
		ExpiresIn:                600, // 10 minutes
		UserName:                 "foo",
		UserUID:                  string("bar"),
		InactivityTimeoutSeconds: defaultTimeout,
	}
	emergTokenClear, emergTokenHash := generateOAuthTokenPair()
	emergToken := oauthv1.OAuthAccessToken{
		ObjectMeta:               metav1.ObjectMeta{Name: emergTokenHash, CreationTimestamp: metav1.Time{Time: testClock.Now()}},
		ClientName:               "quickClient",
		ExpiresIn:                600, // 10 minutes
		UserName:                 "foo",
		UserUID:                  string("bar"),
		InactivityTimeoutSeconds: 5, // super short timeout
	}
	fakeOAuthClient := oauthfake.NewSimpleClientset(&testToken, &quickToken, &slowToken, &emergToken, &testClient, &quickClient, &slowClient)
	fakeUserClient := userfake.NewSimpleClientset(&userv1.User{ObjectMeta: metav1.ObjectMeta{Name: "foo", UID: "bar"}})
	accessTokenGetter := fakeOAuthClient.OauthV1().OAuthAccessTokens()
	oauthClients := fakeOAuthClient.OauthV1().OAuthClients()
	lister := &fakeOAuthClientLister{
		clients: oauthClients,
	}

	timeouts := NewTimeoutValidator(accessTokenGetter, lister, defaultTimeout, minTimeout)

	// inject fake clock, which has some interesting properties
	// 1. A sleep will cause at most one ticker event, regardless of how long the sleep was
	// 2. The clock will hold one tick event and will drop the next one if something does not consume it first
	timeouts.clock = testClock

	// decorate flush
	// The fake clock 1. and 2. require that we issue a wait(t, timeoutsSync) after each testClock.Sleep that causes a tick
	originalFlush := timeouts.flushHandler
	timeoutsSync := make(chan struct{})
	timeouts.flushHandler = func(flushHorizon time.Time) {
		originalFlush(flushHorizon)
		timeoutsSync <- struct{}{} // signal that flush is complete so we never race against it
	}

	// decorate putToken
	// We must issue a wait(t, putTokenSync) after each call to checkToken that should be successful
	originalPutToken := timeouts.putTokenHandler
	putTokenSync := make(chan struct{})
	timeouts.putTokenHandler = func(td *tokenData) {
		originalPutToken(td)
		putTokenSync <- struct{}{} // signal that putToken is complete so we never race against it
	}

	// add some padding to all sleep invocations to make sure we are not failing on any boundary values
	buffer := time.Nanosecond

	tokenAuthenticator := NewTokenAuthenticator(accessTokenGetter, fakeUserClient.UserV1().Users(), NoopGroupMapper{}, nil, timeouts)

	go timeouts.Run(stopCh)

	// TIME: 0 seconds have passed here

	// first time should succeed for all
	checkToken(t, testTokenClear, testTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	checkToken(t, quickTokenClear, quickTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync) // from emergency flush because quickToken has a short enough timeout

	checkToken(t, slowTokenClear, slowTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	// this should cause an emergency flush, if not the next auth will fail,
	// as the token will be timed out
	checkToken(t, emergTokenClear, emergTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync) // from emergency flush because emergToken has a super short timeout

	// wait 6 seconds
	testClock.Sleep(5*time.Second + buffer)

	// a tick happens every 3 seconds
	wait(t, timeoutsSync)

	// TIME: 6th second

	// See if emergency flush happened
	checkToken(t, emergTokenClear, emergTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync) // from emergency flush because emergToken has a super short timeout

	// wait for timeout (minTimeout + 1 - the previously waited 6 seconds)
	testClock.Sleep(time.Duration(minTimeout-5)*time.Second + buffer)
	wait(t, timeoutsSync)

	// TIME: 11th second

	// now we change the testClient and see if the testToken will still be
	// valid instead of timing out
	changeClient, ret := oauthClients.Get(context.TODO(), "testClient", metav1.GetOptions{})
	if ret != nil {
		t.Error("Failed to get testClient")
	} else {
		longTimeout := int32(20)
		changeClient.AccessTokenInactivityTimeoutSeconds = &longTimeout
		_, ret = oauthClients.Update(context.TODO(), changeClient, metav1.UpdateOptions{})
		if ret != nil {
			t.Error("Failed to update testClient")
		}
	}

	// this should fail, thus no call to wait(t, putTokenSync)
	checkToken(t, quickTokenClear, quickTokenHash, tokenAuthenticator, accessTokenGetter, testClock, false)

	// while this should get updated
	checkToken(t, testTokenClear, testTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync)

	// wait for timeout
	testClock.Sleep(time.Duration(clientTimeout+1)*time.Second + buffer)

	// 16 seconds equals 5 more flushes, but the fake clock will only tick once during this time
	wait(t, timeoutsSync)

	// TIME: 27th second

	// this should get updated
	checkToken(t, slowTokenClear, slowTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync)

	// while this should not fail
	checkToken(t, testTokenClear, testTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync)
	// and should be updated to last at least till the 31st second
	token, err := accessTokenGetter.Get(context.TODO(), testTokenHash, metav1.GetOptions{})
	if err != nil {
		t.Error("Failed to get testToken")
	} else {
		if token.InactivityTimeoutSeconds < 31 {
			t.Errorf("Expected timeout in more than 31 seconds, found: %d", token.InactivityTimeoutSeconds)
		}
	}

	//now change testClient again, so that tokens do not expire anymore
	changeclient, ret := oauthClients.Get(context.TODO(), "testClient", metav1.GetOptions{})
	if ret != nil {
		t.Error("Failed to get testClient")
	} else {
		changeclient.AccessTokenInactivityTimeoutSeconds = new(int32)
		_, ret = oauthClients.Update(context.TODO(), changeclient, metav1.UpdateOptions{})
		if ret != nil {
			t.Error("Failed to update testClient")
		}
	}

	// and wait until test token should time out, and has been flushed for sure
	testClock.Sleep(time.Duration(minTimeout)*time.Second + buffer)
	wait(t, timeoutsSync)

	// while this should not fail
	checkToken(t, testTokenClear, testTokenHash, tokenAuthenticator, accessTokenGetter, testClock, true)
	wait(t, putTokenSync)

	wait(t, timeoutsSync)

	// and should be updated to have a ZERO timeout
	token, err = accessTokenGetter.Get(context.TODO(), testTokenHash, metav1.GetOptions{})
	if err != nil {
		t.Error("Failed to get testToken")
	} else {
		if token.InactivityTimeoutSeconds != 0 {
			t.Errorf("Expected timeout of 0 seconds, found: %d", token.InactivityTimeoutSeconds)
		}
	}
}

type fakeOAuthClientLister struct {
	clients oauthclient.OAuthClientInterface
}

func (f fakeOAuthClientLister) Get(name string) (*oauthv1.OAuthClient, error) {
	return f.clients.Get(context.TODO(), name, metav1.GetOptions{})
}

func (f fakeOAuthClientLister) List(selector labels.Selector) ([]*oauthv1.OAuthClient, error) {
	panic("not used")
}

func checkToken(t *testing.T, tokenClear, tokenHash string, authf authenticator.Token, tokens oauthclient.OAuthAccessTokenInterface, current clock.Clock, present bool) {
	t.Helper()
	userInfo, found, err := authf.AuthenticateToken(context.TODO(), tokenClear)
	if present {
		if !found {
			t.Errorf("Did not find token %s!", tokenClear)
		}
		if err != nil {
			t.Errorf("Unexpected error checking for token %s: %v", tokenClear, err)
		}
		if userInfo == nil {
			t.Errorf("Did not get a user for token %s!", tokenClear)
		}
	} else {
		if found {
			token, tokenErr := tokens.Get(context.TODO(), tokenHash, metav1.GetOptions{})
			if tokenErr != nil {
				t.Fatal(tokenErr)
			}
			t.Errorf("Found token (created=%s, timeout=%di, now=%s), but it should be gone!",
				token.CreationTimestamp, token.InactivityTimeoutSeconds, current.Now())
		}
		if err != errTimedout {
			t.Errorf("Unexpected error checking absence of token %s: %v", tokenHash, err)
		}
		if userInfo != nil {
			t.Errorf("Unexpected user checking absence of token %s: %v", tokenHash, userInfo)
		}
	}
}

func wait(t *testing.T, c chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(30 * time.Second):
		t.Fatal("failed to see channel event")
	}
}
