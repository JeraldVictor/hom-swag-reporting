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

func paymentHistoryPositiveTotalExpr() bson.M {
	return bson.M{"$reduce": bson.M{
		"input":        paymentHistoryExpr(),
		"initialValue": 0,
		"in": bson.M{"$add": bson.A{
			"$$value",
			bson.M{"$cond": bson.A{
				bson.M{"$and": bson.A{
					bson.M{"$gt": bson.A{"$$this.amount", 0}},
					bson.M{"$ne": bson.A{
						bson.M{"$toLower": bson.M{"$ifNull": bson.A{"$$this.label", ""}}},
						"tip",
					}},
				}},
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
				bson.M{"$lt": bson.A{"$$this.amount", 0}},
				bson.M{"$abs": "$$this.amount"},
				0,
			}},
		}},
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
					bson.M{"$gt": bson.A{"$$this.amount", 0}},
					bson.M{"$ne": bson.A{
						bson.M{"$toLower": bson.M{"$ifNull": bson.A{"$$this.label", ""}}},
						"tip",
					}},
					bson.M{"$in": bson.A{
						bson.M{"$toLower": bson.M{"$ifNull": bson.A{"$$this.method", ""}}},
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
		legacyPaidExpr(),
	}}
}

func paymentRefundExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryRefundExpr(),
		bson.M{"$add": bson.A{
			bson.M{"$ifNull": bson.A{"$payment.refund_amount", 0}},
			bson.M{"$ifNull": bson.A{"$payment.partial_refund_amount", 0}},
		}},
	}}
}

func paymentCashExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"cash", "cod"}),
		bson.M{"$ifNull": bson.A{"$payment.cod_amount", bson.M{"$ifNull": bson.A{"$cod_collected_amount", 0}}}},
	}}
}

func paymentUpiExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"upi"}),
		bson.M{"$ifNull": bson.A{"$payment.upi_amount", 0}},
	}}
}

func paymentOnlineExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"online", "card", "other", "wallet"}),
		bson.M{"$ifNull": bson.A{"$payment.online_amount", 0}},
	}}
}

func paymentBankTransferExpr() bson.M {
	return bson.M{"$cond": bson.A{
		hasPaymentHistoryExpr(),
		paymentHistoryMethodTotalExpr(bson.A{"bank_transfer", "bank transfer", "bank"}),
		bson.M{"$ifNull": bson.A{"$payment.bank_transfer_amount", 0}},
	}}
}
