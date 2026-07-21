package static

import "go.mongodb.org/mongo-driver/bson"

func paymentHistoryExpr() bson.M {
	return bson.M{"$ifNull": bson.A{"$payment.history", bson.A{}}}
}

func hasPaymentHistoryExpr() bson.M {
	return bson.M{"$gt": bson.A{bson.M{"$size": paymentHistoryExpr()}, 0}}
}

func legacyPaidExpr() bson.M {
	return bson.M{"$ifNull": bson.A{
		"$payment.actual_paid_amount",
		bson.M{"$ifNull": bson.A{"$payment.amount_paid", 0}},
	}}
}

func normalizedPaymentTextExpr(value any) bson.M {
	return bson.M{"$replaceAll": bson.M{
		"input": bson.M{"$replaceAll": bson.M{
			"input": bson.M{"$trim": bson.M{
				"input": bson.M{"$toLower": bson.M{"$ifNull": bson.A{value, ""}}},
			}},
			"find":        "-",
			"replacement": "_",
		}},
		"find":        " ",
		"replacement": "_",
	}}
}

func legacyCodAmountExpr() bson.M {
	return bson.M{"$ifNull": bson.A{
		"$payment.cod_amount",
		bson.M{"$ifNull": bson.A{"$cod_collected_amount", 0}},
	}}
}

func legacyUpiAmountExpr() bson.M {
	return bson.M{"$ifNull": bson.A{"$payment.upi_amount", 0}}
}

func legacyOnlineAmountExpr() bson.M {
	return bson.M{"$ifNull": bson.A{"$payment.online_amount", 0}}
}

func legacyBankTransferAmountExpr() bson.M {
	return bson.M{"$ifNull": bson.A{"$payment.bank_transfer_amount", 0}}
}

func legacySplitAmountExpr() bson.M {
	return bson.M{"$add": bson.A{legacyCodAmountExpr(), legacyUpiAmountExpr()}}
}

func legacyRemainingPaidExpr() bson.M {
	return bson.M{"$max": bson.A{
		0,
		bson.M{"$subtract": bson.A{legacyPaidExpr(), legacySplitAmountExpr()}},
	}}
}

func legacyPaymentMethodExpr() bson.M {
	return normalizedPaymentTextExpr("$payment.method")
}

func legacyRemainingForMethodsExpr(methods bson.A) bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{"$in": bson.A{legacyPaymentMethodExpr(), methods}},
		legacyRemainingPaidExpr(),
		0,
	}}
}

func legacyRemainingForOnlineExpr() bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{"$not": bson.A{
			bson.M{"$in": bson.A{
				legacyPaymentMethodExpr(),
				bson.A{"cash", "cod", "upi", "bank_transfer", "bank"},
			}},
		}},
		legacyRemainingPaidExpr(),
		0,
	}}
}

func legacyMethodBucketOrExplicitExpr(methods bson.A, explicitAmount bson.M) bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{"$gt": bson.A{legacyRemainingPaidExpr(), 0}},
		legacyRemainingForMethodsExpr(methods),
		explicitAmount,
	}}
}

func legacyOnlineBucketOrExplicitExpr() bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{"$gt": bson.A{legacyRemainingPaidExpr(), 0}},
		legacyRemainingForOnlineExpr(),
		legacyOnlineAmountExpr(),
	}}
}

func legacyReceivedFallbackExpr() bson.M {
	return bson.M{"$cond": bson.A{
		bson.M{"$gt": bson.A{legacyPaidExpr(), 0}},
		legacyPaidExpr(),
		bson.M{"$add": bson.A{
			legacyCodAmountExpr(),
			legacyUpiAmountExpr(),
			legacyOnlineAmountExpr(),
			legacyBankTransferAmountExpr(),
		}},
	}}
}

func paymentHistoryPositiveTotalExpr() bson.M {
	return bson.M{"$reduce": bson.M{
		"input":        paymentHistoryExpr(),
		"initialValue": 0,
		"in": bson.M{"$add": bson.A{
			"$$value",
			bson.M{"$cond": bson.A{
				paymentHistoryCountablePaymentCond(),
				"$$this.amount",
				0,
			}},
		}},
	}}
}

func paymentHistoryRefundExpr() bson.M {
	return bson.M{"$reduce": bson.M{
		"input":        paymentHistoryExpr(),
		"initialValue": 0,
		"in": bson.M{"$add": bson.A{
			"$$value",
			bson.M{"$cond": bson.A{
				bson.M{"$or": bson.A{
					bson.M{"$lt": bson.A{"$$this.amount", 0}},
					paymentHistoryLabelMatchesExpr("refund"),
				}},
				bson.M{"$abs": "$$this.amount"},
				0,
			}},
		}},
	}}
}

func paymentHistoryLabelExpr() bson.M {
	return normalizedPaymentTextExpr("$$this.label")
}

func paymentHistoryMethodExpr() bson.M {
	return normalizedPaymentTextExpr("$$this.method")
}

func paymentHistoryBucketTextExpr() bson.M {
	label := paymentHistoryLabelExpr()
	return bson.M{"$cond": bson.A{
		bson.M{"$ne": bson.A{paymentHistoryMethodExpr(), ""}},
		paymentHistoryMethodExpr(),
		bson.M{"$cond": bson.A{
			bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)upi(_|$)"}},
			"upi",
			bson.M{"$cond": bson.A{
				bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)bank(_transfer)?(_|$)"}},
				"bank_transfer",
				bson.M{"$cond": bson.A{
					bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)card(_|$)"}},
					"card",
					bson.M{"$cond": bson.A{
						bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)wallet(_|$)"}},
						"wallet",
						bson.M{"$cond": bson.A{
							bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)online(_|$)"}},
							"online",
							bson.M{"$cond": bson.A{
								bson.M{"$regexMatch": bson.M{"input": label, "regex": "(^|_)(cash|cod)(_|$)"}},
								"cash",
								label,
							}},
						}},
					}},
				}},
			}},
		}},
	}}
}

func paymentHistoryLabelMatchesExpr(regex string) bson.M {
	return bson.M{"$regexMatch": bson.M{
		"input": paymentHistoryLabelExpr(),
		"regex": regex,
	}}
}

func paymentHistoryCountablePaymentCond() bson.M {
	return bson.M{"$and": bson.A{
		bson.M{"$or": bson.A{
			bson.M{"$gt": bson.A{"$$this.amount", 0}},
			bson.M{"$and": bson.A{
				bson.M{"$eq": bson.A{paymentHistoryLabelExpr(), "reconciliation_adjustment"}},
				bson.M{"$ne": bson.A{"$$this.amount", 0}},
			}},
		}},
		bson.M{"$ne": bson.A{paymentHistoryLabelExpr(), "tip"}},
		bson.M{"$not": bson.A{paymentHistoryLabelMatchesExpr("refund")}},
		bson.M{"$not": bson.A{paymentHistoryLabelMatchesExpr("cancellation_(fee|charge)")}},
	}}
}

func paymentHistoryMethodTotalExpr(methods bson.A) bson.M {
	return bson.M{"$reduce": bson.M{
		"input":        paymentHistoryExpr(),
		"initialValue": 0,
		"in": bson.M{"$add": bson.A{
			"$$value",
			bson.M{"$cond": bson.A{
				bson.M{"$and": bson.A{
					paymentHistoryCountablePaymentCond(),
					bson.M{"$in": bson.A{
						paymentHistoryBucketTextExpr(),
						methods,
					}},
				}},
				"$$this.amount",
				0,
			}},
		}},
	}}
}

func paymentReceivedExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryPositiveTotalExpr(),
		legacyReceivedFallbackExpr(),
	}}
}

func paymentRefundExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryRefundExpr(),
		bson.M{"$add": bson.A{
			bson.M{"$ifNull": bson.A{"$payment.refund_amount", 0}},
			bson.M{"$ifNull": bson.A{"$payment.partial_refund_amount", 0}},
			bson.M{"$ifNull": bson.A{
				"$payment.cancellation_refund_amount",
				bson.M{"$ifNull": bson.A{"$cancellation_refund_amount", 0}},
			}},
		}},
	}}
}

func paymentCashExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"cash", "cod"}),
		bson.M{"$add": bson.A{
			legacyCodAmountExpr(),
			legacyRemainingForMethodsExpr(bson.A{"cash", "cod"}),
		}},
	}}
}

func paymentUpiExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"upi"}),
		bson.M{"$add": bson.A{
			legacyUpiAmountExpr(),
			legacyRemainingForMethodsExpr(bson.A{"upi"}),
		}},
	}}
}

func paymentOnlineExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"online", "card", "other", "wallet"}),
		legacyOnlineBucketOrExplicitExpr(),
	}}
}

func paymentBankTransferExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"bank_transfer", "bank"}),
		legacyMethodBucketOrExplicitExpr(
			bson.A{"bank_transfer", "bank"},
			legacyBankTransferAmountExpr(),
		),
	}}
}
