package gamepostgres

import "testing"

func TestTopicAuthorizationSeparatesActorCompanyAndPublicFacts(t *testing.T) {
	t.Parallel()

	actorID := "10000000-0000-4000-8000-000000000001"
	companyID := "20000000-0000-4000-8000-000000000001"
	cases := []struct {
		topic string
		want  bool
	}{
		{topic: "public", want: true},
		{topic: "public.market.water", want: true},
		{topic: "player:" + actorID, want: true},
		{topic: "player:" + actorID + ":alerts", want: true},
		{topic: "company:" + companyID, want: true},
		{topic: "company." + companyID + ".production", want: true},
		{topic: "player:10000000-0000-4000-8000-000000000002", want: false},
		{topic: "company:20000000-0000-4000-8000-000000000002", want: false},
	}
	for _, testCase := range cases {
		if got := topicAuthorized(testCase.topic, actorID, []string{companyID}); got != testCase.want {
			t.Errorf("topicAuthorized(%q) = %v, want %v", testCase.topic, got, testCase.want)
		}
	}
}
