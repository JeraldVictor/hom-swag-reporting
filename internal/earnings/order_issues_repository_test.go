package earnings

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func issueDocument(issue OrderReconciliationIssue) bson.D {
	return bson.D{
		{Key: "_id", Value: issue.ID}, {Key: "issue_key", Value: issue.IssueKey}, {Key: "office_id", Value: issue.OfficeID},
		{Key: "issue_type", Value: issue.IssueType}, {Key: "severity", Value: issue.Severity}, {Key: "status", Value: issue.Status},
		{Key: "order_id", Value: issue.OrderID}, {Key: "order_number", Value: issue.OrderNumber}, {Key: "service_date", Value: issue.ServiceDate},
		{Key: "expected_paise", Value: issue.ExpectedPaise}, {Key: "actual_paise", Value: issue.ActualPaise}, {Key: "difference_paise", Value: issue.DifferencePaise},
		{Key: "first_detected_at", Value: time.Now().UTC()}, {Key: "last_detected_at", Value: time.Now().UTC()},
	}
}

func issueFixture() OrderReconciliationIssue {
	return OrderReconciliationIssue{
		ID: primitive.NewObjectID(), IssueKey: "office:order:payment", OfficeID: primitive.NewObjectID(), OrderID: primitive.NewObjectID(),
		IssueType: OrderIssuePaymentMismatch, Severity: "high", Status: OrderIssueOpen, OrderNumber: "O-1", ServiceDate: "2026-07-21",
		ExpectedPaise: 100000, ActualPaise: 104500, DifferencePaise: 4500,
	}
}

func issueSourceDocument(issue OrderReconciliationIssue, total, paid float64, history bool) bson.D {
	payment := bson.D{{Key: "method", Value: "Online"}}
	if history {
		payment = append(payment, bson.E{Key: "history", Value: bson.A{bson.D{{Key: "label", Value: "Payment"}, {Key: "amount", Value: paid}}}})
	}
	return bson.D{
		{Key: "_id", Value: issue.OrderID}, {Key: "office_id", Value: issue.OfficeID}, {Key: "order_number", Value: issue.OrderNumber},
		{Key: "status", Value: "completed"}, {Key: "booking_info", Value: bson.D{{Key: "date", Value: issue.ServiceDate}}},
		{Key: "subtotal", Value: 1000.0}, {Key: "total", Value: total}, {Key: "payment", Value: payment},
	}
}

func TestRepositoryUpsertAndListOrderIssues(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	issue := issueFixture()
	ns := func(mt *mtest.T) string { return mt.DB.Name() + "." + orderIssueCollection }

	mt.Run("insert", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))
		stored, created, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue)
		if err != nil || !created || stored.ID.IsZero() {
			mt.Fatalf("stored=%+v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("insert error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch), commandError())
		if _, _, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue); err == nil {
			mt.Fatal("expected insert error")
		}
	})
	mt.Run("duplicate insert retries", func(mt *mtest.T) {
		existing := issue
		existing.Status = OrderIssueOpen
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch),
			mtest.CreateWriteErrorsResponse(mtest.WriteError{Code: 11000, Message: "duplicate"}),
			mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, issueDocument(existing)),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(existing)}),
		)
		stored, created, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue)
		if err != nil || created || stored.ID != issue.ID {
			mt.Fatalf("stored=%+v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("reopens resolved issue", func(mt *mtest.T) {
		existing := issue
		existing.Status = OrderIssueResolved
		updated := issue
		updated.Status = OrderIssueOpen
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, issueDocument(existing)), mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(updated)}))
		stored, created, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue)
		if err != nil || created || stored.Status != OrderIssueOpen {
			mt.Fatalf("stored=%+v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("accepted issue reopens when variance changes", func(mt *mtest.T) {
		existing := issue
		existing.Status, existing.DifferencePaise = OrderIssueAccepted, 1
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, issueDocument(existing)), mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(issue)}))
		if _, _, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue); err != nil {
			mt.Fatal(err)
		}
	})
	mt.Run("lookup and update errors", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue); err == nil {
			mt.Fatal("expected lookup error")
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, issueDocument(issue)), commandError())
		if _, _, err := NewRepository(mt.DB).upsertOrderIssue(context.Background(), issue); err == nil {
			mt.Fatal("expected update error")
		}
	})
	mt.Run("list filters and errors", func(mt *mtest.T) {
		filter := OrderIssueFilter{OfficeID: issue.OfficeID, StartDate: "2026-07-01", EndDate: "2026-07-31", Status: OrderIssueOpen, IssueType: issue.IssueType, Severity: "high", Search: "O.+", Page: 2, Limit: 20}
		mt.AddMockResponses(countResponse(ns(mt), 1), mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, issueDocument(issue)))
		rows, total, err := NewRepository(mt.DB).ListOrderIssues(context.Background(), filter)
		if err != nil || total != 1 || len(rows) != 1 {
			mt.Fatalf("rows=%v total=%d err=%v", rows, total, err)
		}
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).ListOrderIssues(context.Background(), OrderIssueFilter{OfficeID: issue.OfficeID, Page: 1, Limit: 20}); err == nil {
			mt.Fatal("expected count error")
		}
		mt.AddMockResponses(countResponse(ns(mt), 0), commandError())
		if _, _, err := NewRepository(mt.DB).ListOrderIssues(context.Background(), filter); err == nil {
			mt.Fatal("expected find error")
		}
		mt.AddMockResponses(countResponse(ns(mt), 1), mtest.CreateCursorResponse(0, ns(mt), mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}))
		if _, _, err := NewRepository(mt.DB).ListOrderIssues(context.Background(), filter); err == nil {
			mt.Fatal("expected decode error")
		}
	})
	if got := regexpQuote(`a.+*(b)[c]{d}^$|?\`); got == `a.+*(b)[c]{d}^$|?\` {
		t.Fatalf("regexp was not quoted: %q", got)
	}
}

func TestRepositoryScanOrderIssues(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	issue := issueFixture()
	ordersNS := func(mt *mtest.T) string { return mt.DB.Name() + ".orders" }
	issuesNS := func(mt *mtest.T) string { return mt.DB.Name() + "." + orderIssueCollection }

	mt.Run("creates detected issue and resolves stale", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ordersNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true)),
			mtest.CreateCursorResponse(0, issuesNS(mt), mtest.FirstBatch),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 2}, bson.E{Key: "nModified", Value: 2}),
		)
		result, err := NewRepository(mt.DB).ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31")
		if err != nil || result.Scanned != 1 || result.Created != 1 || result.Open != 1 || result.AutoResolved != 2 || result.TotalVariance != 4500 {
			mt.Fatalf("result=%+v err=%v", result, err)
		}
	})
	mt.Run("updates issue and skips invalid source", func(mt *mtest.T) {
		invalid := issueSourceDocument(issue, 1000, 1000, true)
		invalid = append(invalid, bson.E{Key: "is_deleted", Value: true})
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ordersNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true), invalid),
			mtest.CreateCursorResponse(0, issuesNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(issue)}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}),
		)
		result, err := NewRepository(mt.DB).ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31")
		if err != nil || result.Scanned != 2 || result.Updated != 1 {
			mt.Fatalf("result=%+v err=%v", result, err)
		}
	})
	mt.Run("find decode and update errors", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		mt.AddMockResponses(commandError())
		if _, err := repo.ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected find error")
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ordersNS(mt), mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}))
		if _, err := repo.ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected decode error")
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ordersNS(mt), mtest.FirstBatch), commandError())
		if _, err := repo.ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected update error")
		}
	})
	mt.Run("cursor iteration error", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(42, ordersNS(mt), mtest.FirstBatch),
			commandError(),
		)
		if _, err := NewRepository(mt.DB).ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected cursor iteration error")
		}
	})
	mt.Run("upsert error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ordersNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true)), commandError())
		if _, err := NewRepository(mt.DB).ScanOrderIssues(context.Background(), issue.OfficeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected upsert error")
		}
	})
}

func TestRepositoryActsOnOrderIssues(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	issue := issueFixture()
	issueNS := func(mt *mtest.T) string { return mt.DB.Name() + "." + orderIssueCollection }
	orderNS := func(mt *mtest.T) string { return mt.DB.Name() + ".orders" }

	mt.Run("not found lookup error closed and unsupported", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch))
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionRecheck}); !errors.Is(err, ErrOrderIssueNotFound) {
			mt.Fatalf("err=%v", err)
		}
		mt.AddMockResponses(commandError())
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionRecheck}); err == nil {
			mt.Fatal("expected lookup error")
		}
		closed := issue
		closed.Status = OrderIssueResolved
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(closed)))
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAccept}); !errors.Is(err, ErrOrderIssueAlreadyClosed) {
			mt.Fatalf("err=%v", err)
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)))
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: "bad"}); !errors.Is(err, ErrOrderIssueUnsupported) {
			mt.Fatalf("err=%v", err)
		}
		invalid := issue
		invalid.IssueType = OrderIssueInvalidTotal
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(invalid)))
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); !errors.Is(err, ErrOrderIssueUnsupported) {
			mt.Fatalf("err=%v", err)
		}
	})
	mt.Run("accept closes", func(mt *mtest.T) {
		accepted := issue
		accepted.Status = OrderIssueAccepted
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)), mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(accepted)}))
		stored, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAccept, Actor: primitive.NewObjectID()})
		if err != nil || stored.Status != OrderIssueAccepted {
			mt.Fatalf("stored=%+v err=%v", stored, err)
		}
	})
	mt.Run("recheck still present", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true)),
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(issue)}),
		)
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionRecheck}); err != nil {
			mt.Fatal(err)
		}
	})
	mt.Run("recheck resolved and source error", func(mt *mtest.T) {
		resolved := issue
		resolved.Status = OrderIssueResolved
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1000, true)),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(resolved)}),
		)
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionRecheck}); err != nil {
			mt.Fatal(err)
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)), commandError())
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionRecheck}); err == nil {
			mt.Fatal("expected source error")
		}
	})
	mt.Run("align rejects legacy and handles source error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)), commandError())
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err == nil {
			mt.Fatal("expected source error")
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)), mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, false)))
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); !errors.Is(err, ErrOrderIssueUnsupported) {
			mt.Fatalf("err=%v", err)
		}
	})
	mt.Run("align applies correction with default method", func(mt *mtest.T) {
		resolved := issue
		resolved.Status = OrderIssueResolved
		source := issueSourceDocument(issue, 1000, 1045, true)
		for i := range source {
			if source[i].Key == "payment" {
				source[i].Value = bson.D{{Key: "method", Value: ""}, {Key: "history", Value: bson.A{bson.D{{Key: "label", Value: "Payment"}, {Key: "amount", Value: 1045.0}}}}}
			}
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, source),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(resolved)}),
		)
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err != nil {
			mt.Fatal(err)
		}
	})
	mt.Run("align update and idempotency errors", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		responses := func(extra ...bson.D) []bson.D {
			base := []bson.D{mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)), mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true))}
			return append(base, extra...)
		}
		mt.AddMockResponses(responses(commandError())...)
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err == nil {
			mt.Fatal("expected update error")
		}
		mt.AddMockResponses(responses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}), commandError())...)
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err == nil {
			mt.Fatal("expected count error")
		}
		mt.AddMockResponses(responses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}), countResponse(orderNS(mt), 0))...)
		if _, err := repo.ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); !errors.Is(err, ErrOrderIssueStillPresent) {
			mt.Fatalf("err=%v", err)
		}
	})
	mt.Run("align idempotent and resolved source", func(mt *mtest.T) {
		resolved := issue
		resolved.Status = OrderIssueResolved
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1045, true)),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}),
			countResponse(orderNS(mt), 1),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(resolved)}),
		)
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err != nil {
			mt.Fatal(err)
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, issueNS(mt), mtest.FirstBatch, issueDocument(issue)),
			mtest.CreateCursorResponse(0, orderNS(mt), mtest.FirstBatch, issueSourceDocument(issue, 1000, 1000, true)),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: issueDocument(resolved)}),
		)
		if _, err := NewRepository(mt.DB).ActOnOrderIssue(context.Background(), issue.OfficeID, issue.ID, OrderIssueActionInput{Action: OrderIssueActionAlign}); err != nil {
			mt.Fatal(err)
		}
	})
	mt.Run("close decode error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).closeOrderIssue(context.Background(), issue, OrderIssueResolved, OrderIssueActionInput{}, 0); err == nil {
			mt.Fatal("expected close error")
		}
	})
}
