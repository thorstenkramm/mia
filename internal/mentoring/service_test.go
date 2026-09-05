package mentoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

type fixture struct {
	database                   *sql.DB
	service                    *Service
	directory                  string
	supervisor, student        string
	mentor, replacement, other string
	course                     string
	now                        time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	database, err := miSQLite.Open(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	ctx := context.Background()
	create := func(username string, roles ...user.Role) string {
		email := ""
		verified := false
		if roles[0] != user.Student {
			email, verified = username+"@example.test", true
		}
		account, createErr := user.Create(ctx, database, user.CreateInput{Username: username,
			Email: email, EmailVerified: verified, PasswordHash: "hash", Language: "en", Country: "US",
			TimeZone: "UTC", Roles: roles})
		require.NoError(t, createErr)
		return account.ID
	}
	value := fixture{database: database, directory: directory, supervisor: create("supervisor", user.Supervisor, user.Student),
		student: create("student", user.Student), mentor: create("mentorone", user.Mentor),
		replacement: create("mentortwo", user.Mentor), other: create("otheruser", user.Mentor),
		course: "cou_00000000-0000-4000-8000-000000000001",
		now:    time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	_, err = database.Exec(`INSERT INTO courses (id, name, name_normalized, is_active, created_at)
		VALUES (?, 'Course', 'course', 1, ?)`, value.course, instant(value.now))
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_supervisors
		(course_id, supervisor_user_id, assigned_at) VALUES (?, ?, ?)`, value.course, value.supervisor, instant(value.now))
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_students
		(id, course_id, student_user_id, joined_at) VALUES ('cst_test', ?, ?, ?)`, value.course, value.student,
		instant(value.now))
	require.NoError(t, err)
	value.service = NewService(database, user.NewService(database, directory, nil, nil), nil)
	value.service.now = func() time.Time { return value.now }
	return value
}

func TestRequestAdmissionGatesDoNotAffectExistingWork(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()

	_, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student, Topic: "Help"})
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, fixture.mentor, fixture.supervisor))
	require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student, fixture.mentor,
		fixture.supervisor))
	_, err = fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student, Topic: "Help"})
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
	created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
		Topic: "  Fractions\r\nplease  ", ProposedFor: timePointer(fixture.now.Add(time.Hour))})
	require.NoError(t, err)
	assert.Empty(t, created.MentorID)
	assert.Equal(t, "Fractions\nplease", created.Topic)

	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, false))
	_, err = fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student, Topic: "Again"})
	assert.ErrorIs(t, err, ErrUnavailable)
	visible, err := fixture.service.Get(ctx, created.ID, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, created.Topic, visible.Topic)
	closed, err := fixture.service.Update(ctx, created.ID, UpdateInput{ClosureReason: reasonCanceled, ActorID: fixture.student})
	require.NoError(t, err)
	assert.Equal(t, reasonCanceled, closed.ClosureReason)
}

func TestTriageResponseSchedulingAndDirectReassignment(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	for _, mentorID := range []string{fixture.mentor, fixture.replacement} {
		require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, mentorID, fixture.supervisor))
		require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student, mentorID,
			fixture.supervisor))
	}
	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
	created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
		Topic: "Algebra", ProposedFor: timePointer(fixture.now.Add(time.Hour))})
	require.NoError(t, err)

	_, err = fixture.service.Update(ctx, created.ID, UpdateInput{MentorID: optionalValue(fixture.mentor),
		ActorID: fixture.supervisor})
	require.NoError(t, err)
	response := "Let's work through it."
	instructions := "Use the school video room."
	meetingURL := "https://meet.example.test/room"
	scheduled := fixture.now.Add(2 * time.Hour)
	responded, err := fixture.service.Update(ctx, created.ID, UpdateInput{ActorID: fixture.mentor,
		Response: optionalValue(response), ScheduledFor: OptionalTime{Set: true, Value: &scheduled},
		MeetingInstructions: optionalValue(instructions), MeetingURL: optionalValue(meetingURL)})
	require.NoError(t, err)
	assert.Equal(t, fixture.mentor, responded.RespondedBy)

	reassigned, err := fixture.service.Update(ctx, created.ID, UpdateInput{MentorID: optionalValue(fixture.replacement),
		ActorID: fixture.supervisor})
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, reassigned.MentorID)
	assert.Equal(t, response, reassigned.Response)
	assert.Equal(t, fixture.mentor, reassigned.RespondedBy)
	assert.Equal(t, scheduled, *reassigned.ScheduledFor)
	assert.Equal(t, meetingURL, reassigned.MeetingURL)

	require.NoError(t, fixture.service.RemoveStudentMentor(ctx, fixture.course, fixture.student,
		fixture.replacement, fixture.supervisor))
	triaged, err := fixture.service.Get(ctx, created.ID, fixture.supervisor)
	require.NoError(t, err)
	assert.Empty(t, triaged.MentorID)
	assert.Nil(t, triaged.ProposedFor)
	assert.Nil(t, triaged.ScheduledFor)
	assert.Empty(t, triaged.MeetingURL)
	assert.Equal(t, response, triaged.Response)
	assert.Equal(t, fixture.mentor, triaged.RespondedBy)
	var triageAudits int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'mentoring.session.triaged' AND actor_user_id = ?
		AND json_extract(metadata, '$.mentoring_session_id') = ?
		AND json_extract(metadata, '$.mentor_id') = ?`, fixture.supervisor, created.ID,
		fixture.replacement).Scan(&triageAudits))
	assert.Equal(t, 1, triageAudits)
}

func TestScheduleCancellationAndCompletionRules(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, fixture.mentor, fixture.supervisor))
	require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student, fixture.mentor,
		fixture.supervisor))
	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
	createScheduled := func() Session {
		created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
			Topic: "Geometry"})
		require.NoError(t, err)
		_, err = fixture.service.Update(ctx, created.ID, UpdateInput{MentorID: optionalValue(fixture.mentor),
			ActorID: fixture.supervisor})
		require.NoError(t, err)
		scheduled := fixture.now.Add(time.Hour)
		created, err = fixture.service.Update(ctx, created.ID, UpdateInput{ScheduledFor: OptionalTime{Set: true,
			Value: &scheduled}, ActorID: fixture.mentor})
		require.NoError(t, err)
		return created
	}

	first := createScheduled()
	_, err := fixture.service.Update(ctx, first.ID, UpdateInput{ClosureReason: reasonCanceled, ActorID: fixture.supervisor})
	assert.ErrorIs(t, err, ErrInvalidState)
	rescheduled := fixture.now.Add(3 * time.Hour)
	first, err = fixture.service.Update(ctx, first.ID, UpdateInput{ScheduledFor: OptionalTime{Set: true,
		Value: &rescheduled}, ActorID: fixture.student})
	require.NoError(t, err)
	assert.Equal(t, rescheduled, *first.ScheduledFor)
	var metadataJSON string
	require.NoError(t, fixture.database.QueryRow(`SELECT metadata FROM audit_events
		WHERE action = 'mentoring.session.rescheduled' AND actor_user_id = ? ORDER BY created_at DESC LIMIT 1`,
		fixture.student).Scan(&metadataJSON))
	var metadata map[string]string
	require.NoError(t, json.Unmarshal([]byte(metadataJSON), &metadata))
	assert.Equal(t, instant(fixture.now.Add(time.Hour)), metadata["previous_scheduled_at"])
	assert.Equal(t, instant(rescheduled), metadata["new_scheduled_at"])
	first, err = fixture.service.Update(ctx, first.ID, UpdateInput{ClosureReason: reasonCanceled, ActorID: fixture.student})
	require.NoError(t, err)
	assert.Equal(t, reasonCanceled, first.ClosureReason)

	second := createScheduled()
	_, err = fixture.service.Update(ctx, second.ID, UpdateInput{ClosureReason: "completed", ActorID: fixture.mentor})
	assert.ErrorIs(t, err, ErrInvalidState)
	fixture.now = fixture.now.Add(2 * time.Hour)
	fixture.service.now = func() time.Time { return fixture.now }
	second, err = fixture.service.Update(ctx, second.ID, UpdateInput{ClosureReason: "completed", ActorID: fixture.mentor})
	require.NoError(t, err)
	assert.Equal(t, "completed", second.ClosureReason)
	_, err = fixture.service.Update(ctx, second.ID, UpdateInput{ClosureReason: reasonCanceled, ActorID: fixture.student})
	assert.True(t, errors.Is(err, ErrInvalidState))
	visible, err := fixture.service.List(ctx, fixture.course, fixture.mentor, ListInput{Limit: 25})
	require.NoError(t, err)
	assert.Len(t, visible.Sessions, 2)
	require.NoError(t, fixture.service.RemoveStudentMentor(ctx, fixture.course, fixture.student, fixture.mentor,
		fixture.supervisor))
	_, err = fixture.service.Get(ctx, second.ID, fixture.mentor)
	assert.ErrorIs(t, err, ErrNotFound)
	revoked, err := fixture.service.List(ctx, fixture.course, fixture.mentor, ListInput{Limit: 25})
	require.NoError(t, err)
	assert.Empty(t, revoked.Sessions)
	_, err = fixture.service.Get(ctx, second.ID, fixture.student)
	require.NoError(t, err)
}

func TestWrongScopeIsHiddenAndCourseRemovalTriagesAllOpenWork(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	require.NoError(t, fixture.service.AssignCourseMentor(ctx, fixture.course, fixture.mentor, fixture.supervisor))
	require.NoError(t, fixture.service.AssignStudentMentor(ctx, fixture.course, fixture.student, fixture.mentor,
		fixture.supervisor))
	require.NoError(t, user.SetMentoringRequestsAllowed(ctx, fixture.database, fixture.student, true))
	created, err := fixture.service.Create(ctx, CreateInput{CourseID: fixture.course, StudentID: fixture.student,
		Topic: "Reading"})
	require.NoError(t, err)
	_, err = fixture.service.Get(ctx, created.ID, fixture.other)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.service.Update(ctx, created.ID, UpdateInput{MentorID: optionalValue(fixture.mentor),
		ActorID: fixture.supervisor})
	require.NoError(t, err)
	_, err = fixture.service.Get(ctx, created.ID, fixture.mentor)
	require.NoError(t, err)
	require.NoError(t, fixture.service.RemoveCourseMentor(ctx, fixture.course, fixture.mentor, fixture.supervisor))
	_, err = fixture.service.Get(ctx, created.ID, fixture.mentor)
	assert.ErrorIs(t, err, ErrNotFound)
	triaged, err := fixture.service.Get(ctx, created.ID, fixture.student)
	require.NoError(t, err)
	assert.Empty(t, triaged.MentorID)
	var assignments int
	require.NoError(t, fixture.database.QueryRow("SELECT COUNT(*) FROM mentor_assignments WHERE mentor_user_id = ?",
		fixture.mentor).Scan(&assignments))
	assert.Zero(t, assignments)
}

func optionalValue(value string) OptionalString { return OptionalString{Set: true, Value: &value} }
func timePointer(value time.Time) *time.Time    { return &value }
