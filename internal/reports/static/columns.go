package static

import "github.com/JeraldVictor/hom-swag-reporting/internal/reports"

var riderCommissionColumns = []reports.Column{
	{Key: "staff_id", Label: "Staff ID", Type: "string", SourcePath: "staff_id", DefaultVisible: false, Filterable: true},
	{Key: "emp_code", Label: "Employee Code", Type: "string", SourcePath: "emp_code", DefaultVisible: true, Sortable: true},
	{Key: "rider_name", Label: "Rider Name", Type: "string", SourcePath: "rider_name", DefaultVisible: true, Sortable: true, Filterable: true},
	{Key: "trip_count", Label: "Trip Count", Type: "number", SourcePath: "trip_count", FormulaID: "rider.completed_trip_count", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "total_distance_km", Label: "Total Distance KM", Type: "number", SourcePath: "total_distance_km", FormulaID: "trip.payable_distance_km.sum", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "petrol_payable", Label: "Petrol Payable", Type: "currency", SourcePath: "petrol_payable", FormulaID: "trip.petrol_payable.sum", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "commission_payable", Label: "Trip Commission", Type: "currency", SourcePath: "commission_payable", FormulaID: "trip.commission_payable.sum", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "leaderboard_rank", Label: "Leaderboard Rank", Type: "string", SourcePath: "leaderboard_rank", FormulaID: "rider.leaderboard_rank", DefaultVisible: true, Sortable: true},
	{Key: "leaderboard_bonus", Label: "Leaderboard Bonus", Type: "currency", SourcePath: "leaderboard_bonus", FormulaID: "rider.leaderboard_bonus", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "total_commission", Label: "Total Commission", Type: "currency", SourcePath: "total_commission", FormulaID: "rider.total_commission", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "total_rider_payable", Label: "Total Rider Payable", Type: "currency", SourcePath: "total_rider_payable", FormulaID: "rider.total_payable", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
}

var petrolWeeklyColumns = []reports.Column{
	{Key: "staff_id", Label: "Staff ID", Type: "string", SourcePath: "staff_id", DefaultVisible: false, Filterable: true},
	{Key: "emp_code", Label: "Employee Code", Type: "string", SourcePath: "emp_code", DefaultVisible: true, Sortable: true},
	{Key: "rider_name", Label: "Rider Name", Type: "string", SourcePath: "rider_name", DefaultVisible: true, Sortable: true, Filterable: true},
	{Key: "total_distance_km", Label: "Total Distance (KM)", Type: "number", SourcePath: "total_distance", FormulaID: "trip.petrol_distance.sum", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "payable_amount", Label: "Payable Amount", Type: "currency", SourcePath: "total_amount", FormulaID: "trip.petrol_payable.sum", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
}

var dailySalesColumns = []reports.Column{
	{Key: "customer_name", Label: "Customer Name", Type: "string", SourcePath: "customer_name", DefaultVisible: true, Filterable: true},
	{Key: "invoice_number", Label: "Invoice Number", Type: "string", SourcePath: "invoice_number", DefaultVisible: true, Filterable: true},
	{Key: "invoice_date", Label: "Invoice Date", Type: "date", SourcePath: "invoice_date", DefaultVisible: true, Sortable: true},
	{Key: "order_number", Label: "Order Num#", Type: "string", SourcePath: "order_number", DefaultVisible: true, Filterable: true},
	{Key: "order_date", Label: "Order Date", Type: "date", SourcePath: "order_date", DefaultVisible: true, Sortable: true},
	{Key: "beautician_unique_code", Label: "Unique Code of Beautician", Type: "string", SourcePath: "beautician_unique_code", DefaultVisible: true},
	{Key: "beautician_pan", Label: "PAN of Beautician", Type: "string", SourcePath: "beautician_pan", Sensitive: true, DefaultVisible: true},
	{Key: "beautician_name", Label: "Beautician Name", Type: "string", SourcePath: "beautician_name", DefaultVisible: true, Filterable: true},
	{Key: "sac_code", Label: "SAC Code", Type: "string", SourcePath: "sac_code", DefaultVisible: true},
	{Key: "total_services_cost", Label: "Total Services cost", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "total_services_cost", FormulaID: "order.subtotal", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "convenience_fees", Label: "Convenience Fees", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "convenience_fees", FormulaID: "order.convenience_fees", ContributesToTotal: true, DefaultVisible: true},
	{Key: "hygiene_fees", Label: "Hygiene Fees", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "hygiene_fees", FormulaID: "order.hygiene_fees", ContributesToTotal: true, DefaultVisible: true},
	{Key: "surge_charges", Label: "Surge Charges", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "surge_charges", FormulaID: "order.surge_amount", ContributesToTotal: true, DefaultVisible: true},
	{Key: "membership_charges", Label: "Membership Charges", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "membership_charges", FormulaID: "order.membership_charge", ContributesToTotal: true, DefaultVisible: true},
	{Key: "cancellation_charges", Label: "Cancellation Charges", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "cancellation_charges", FormulaID: "order.cancellation_charge", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_value", Label: "Total Value", Type: "currency", Group: "Gross Revenue Including GST", SourcePath: "total_value", FormulaID: "daily_sales.total_value", ContributesToTotal: true, DefaultVisible: true},
	{Key: "membership_discount", Label: "Membership discount", Type: "currency", Group: "Discount Offered Including GST", SourcePath: "membership_discount", FormulaID: "order.membership_discount", ContributesToTotal: true, DefaultVisible: true},
	{Key: "special_discount", Label: "Special discount", Type: "currency", Group: "Discount Offered Including GST", SourcePath: "special_discount", FormulaID: "order.one_time_discount", ContributesToTotal: true, DefaultVisible: true},
	{Key: "coupon_discount", Label: "Coupon Discount", Type: "currency", Group: "Discount Offered Including GST", SourcePath: "coupon_discount", FormulaID: "order.coupon_discount", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_discount_provided", Label: "Total Discount Provided", Type: "currency", Group: "Discount Offered Including GST", SourcePath: "total_discount_provided", FormulaID: "daily_sales.total_discount", ContributesToTotal: true, DefaultVisible: true},
	{Key: "net_receivable_including_gst", Label: "Net Receivable including GST", Type: "currency", SourcePath: "net_receivable_including_gst", FormulaID: "daily_sales.net_receivable", ContributesToTotal: true, DefaultVisible: true},
	{Key: "tips", Label: "Tips", Type: "currency", SourcePath: "tips", FormulaID: "payment.tips", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_receivable_including_tips", Label: "Total receivable including tips", Type: "currency", SourcePath: "total_receivable_including_tips", FormulaID: "daily_sales.total_receivable", ContributesToTotal: true, DefaultVisible: true},
	{Key: "online", Label: "Online", Type: "currency", SourcePath: "online", FormulaID: "payment.online", ContributesToTotal: true, DefaultVisible: true},
	{Key: "cash", Label: "Cash", Type: "currency", SourcePath: "cash", FormulaID: "payment.cash", ContributesToTotal: true, DefaultVisible: true},
	{Key: "bank_transfer", Label: "Bank Transfer", Type: "currency", SourcePath: "bank_transfer", FormulaID: "payment.bank_transfer", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_received", Label: "Total Received", Type: "currency", SourcePath: "total_received", FormulaID: "payment.total_received", ContributesToTotal: true, DefaultVisible: true},
	{Key: "payment_gateway_charges_tips", Label: "Payment Gateway Charges-Tips", Type: "currency", SourcePath: "payment_gateway_charges_tips", FormulaID: "payment.gateway_charges_tips", ContributesToTotal: true, DefaultVisible: true},
	{Key: "payment_gateway_charges_others", Label: "Payment Gateway Charges-Others", Type: "currency", SourcePath: "payment_gateway_charges_others", FormulaID: "payment.gateway_charges_others", ContributesToTotal: true, DefaultVisible: true},
	{Key: "net_receivable_bank", Label: "Net Receivable Bank", Type: "currency", SourcePath: "net_receivable_bank", FormulaID: "daily_sales.net_receivable_bank", ContributesToTotal: true, DefaultVisible: true},
	{Key: "net_tips_payable", Label: "Net tips Payable", Type: "currency", SourcePath: "net_tips_payable", FormulaID: "daily_sales.net_tips_payable", ContributesToTotal: true, DefaultVisible: true},
}

var beauticianCommissionColumns = []reports.Column{
	{Key: "staff_id", Label: "Staff ID", Type: "string", SourcePath: "staff_id", DefaultVisible: false, Filterable: true},
	{Key: "emp_code", Label: "Employee Code", Type: "string", SourcePath: "emp_code", DefaultVisible: true, Sortable: true},
	{Key: "name", Label: "Beautician Name", Type: "string", SourcePath: "name", DefaultVisible: true, Sortable: true, Filterable: true},
	{Key: "order_count", Label: "Order Count", Type: "number", SourcePath: "order_count", FormulaID: "beautician.completed_order_count", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "total_revenue", Label: "Total Revenue", Type: "currency", SourcePath: "total_revenue", FormulaID: "beautician.completed_revenue", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "target1", Label: "Target 1", Type: "currency", SourcePath: "target1", DefaultVisible: true},
	{Key: "target1_achieved", Label: "Target 1 Achieved", Type: "boolean", SourcePath: "target1_achieved", FormulaID: "beautician.target1_achieved", DefaultVisible: true},
	{Key: "target2", Label: "Target 2", Type: "currency", SourcePath: "target2", DefaultVisible: true},
	{Key: "target2_achieved", Label: "Target 2 Achieved", Type: "boolean", SourcePath: "target2_achieved", FormulaID: "beautician.target2_achieved", DefaultVisible: true},
	{Key: "special_commission", Label: "Special Commission", Type: "currency", SourcePath: "special_commission", FormulaID: "commission.special", ContributesToTotal: true, DefaultVisible: true},
	{Key: "payable_general_commission", Label: "Payable General Commission", Type: "currency", SourcePath: "payable_general_commission", FormulaID: "commission.payable_general", ContributesToTotal: true, DefaultVisible: true},
	{Key: "potential_general_commission", Label: "Potential General Commission", Type: "currency", SourcePath: "potential_general_commission", FormulaID: "commission.potential_general", ContributesToTotal: true, DefaultVisible: false},
	{Key: "upgrade_addon_commission", Label: "Upgrade/Add-on Commission", Type: "currency", SourcePath: "upgrade_addon_commission", FormulaID: "commission.upgrade_addon", ContributesToTotal: true, DefaultVisible: true},
	{Key: "refunded", Label: "Refunded", Type: "currency", SourcePath: "refunded", FormulaID: "payment.refunded", ContributesToTotal: true, DefaultVisible: true},
	{Key: "target2_bonus", Label: "Target 2 Bonus", Type: "currency", SourcePath: "target2_bonus", FormulaID: "beautician.target2_bonus", ContributesToTotal: true, DefaultVisible: true},
	{Key: "potential_target2_bonus", Label: "Potential Target 2 Bonus", Type: "currency", SourcePath: "potential_target2_bonus", FormulaID: "beautician.potential_target2_bonus", ContributesToTotal: true, DefaultVisible: false},
	{Key: "leaderboard_rank", Label: "Leaderboard Rank", Type: "string", SourcePath: "leaderboard_rank", FormulaID: "beautician.leaderboard_rank", DefaultVisible: true},
	{Key: "leaderboard_bonus", Label: "Leaderboard Bonus", Type: "currency", SourcePath: "leaderboard_bonus", FormulaID: "beautician.leaderboard_bonus", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_commission", Label: "Total Commission", Type: "currency", SourcePath: "total_commission", FormulaID: "beautician.total_commission", ContributesToTotal: true, DefaultVisible: true},
	{Key: "estimated_target1_commission", Label: "Estimated Commission at Target 1", Type: "currency", SourcePath: "estimated_target1_commission", FormulaID: "beautician.estimated_target1_commission", DefaultVisible: false},
	{Key: "estimated_target2_commission", Label: "Estimated Commission at Target 2", Type: "currency", SourcePath: "estimated_target2_commission", FormulaID: "beautician.estimated_target2_commission", DefaultVisible: false},
}

var staffSummaryColumns = []reports.Column{
	{Key: "emp_code", Label: "Employee Code", Type: "string", SourcePath: "emp_code", DefaultVisible: true, Sortable: true},
	{Key: "name", Label: "Staff Name", Type: "string", SourcePath: "name", DefaultVisible: true, Sortable: true, Filterable: true},
	{Key: "role", Label: "Role", Type: "string", SourcePath: "role", DefaultVisible: true, Filterable: true},
	{Key: "leave_count", Label: "Leave Count", Type: "number", SourcePath: "leave_count", FormulaID: "staff.leave_count", ContributesToTotal: true, DefaultVisible: true},
	{Key: "ot_count", Label: "Overtime Count", Type: "number", SourcePath: "ot_count", FormulaID: "staff.overtime_count", ContributesToTotal: true, DefaultVisible: true},
}

var codPendingColumns = []reports.Column{
	{Key: "order_number", Label: "Order Number", Type: "string", SourcePath: "order_number", DefaultVisible: true, Filterable: true},
	{Key: "customer_name", Label: "Customer Name", Type: "string", SourcePath: "customer_name", DefaultVisible: true, Filterable: true},
	{Key: "order_date", Label: "Order Date", Type: "date", SourcePath: "order_date", DefaultVisible: true, Sortable: true},
	{Key: "cod_status", Label: "COD Status", Type: "string", SourcePath: "cod_status", DefaultVisible: true, Filterable: true},
	{Key: "cod_collected_amount", Label: "COD Collected Amount", Type: "currency", SourcePath: "cod_collected_amount", FormulaID: "payment.cod_pending_settlement", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
}

var customerBookingColumns = []reports.Column{
	{Key: "customer_id", Label: "Customer ID", Type: "string", SourcePath: "customer_id", DefaultVisible: false, Filterable: true},
	{Key: "customer_name", Label: "Customer Name", Type: "string", SourcePath: "customer_name", DefaultVisible: true, Sortable: true, Filterable: true},
	{Key: "phone", Label: "Phone", Type: "string", SourcePath: "phone", Sensitive: true, DefaultVisible: true, Filterable: true},
	{Key: "alternative_phone", Label: "Alternative Phone", Type: "string", SourcePath: "alternative_phone", Sensitive: true, DefaultVisible: false, Filterable: true},
	{Key: "email", Label: "Email", Type: "string", SourcePath: "email", Sensitive: true, DefaultVisible: false, Filterable: true},
	{Key: "gender", Label: "Gender", Type: "string", SourcePath: "gender", DefaultVisible: false, Filterable: true},
	{Key: "customer_status", Label: "Customer Status", Type: "string", SourcePath: "customer_status", DefaultVisible: true, Filterable: true},
	{Key: "registered_at", Label: "Registered Date", Type: "date", SourcePath: "registered_at", DefaultVisible: false, Sortable: true},
	{Key: "last_booking_date", Label: "Last Booking Date", Type: "date", SourcePath: "last_booking_date", DefaultVisible: true, Sortable: true},
	{Key: "booking_count", Label: "Bookings in Duration", Type: "number", SourcePath: "booking_count", ContributesToTotal: true, DefaultVisible: true, Sortable: true},
	{Key: "zones", Label: "Zones", Type: "string", SourcePath: "zones", DefaultVisible: true, Filterable: true},
	{Key: "address_count", Label: "Saved Address Count", Type: "number", SourcePath: "address_count", ContributesToTotal: true, DefaultVisible: false, Sortable: true},
	{Key: "default_address", Label: "Default Address", Type: "string", SourcePath: "default_address", Sensitive: true, DefaultVisible: false},
	{Key: "city", Label: "City", Type: "string", SourcePath: "city", DefaultVisible: false, Filterable: true},
	{Key: "state", Label: "State", Type: "string", SourcePath: "state", DefaultVisible: false, Filterable: true},
	{Key: "pincode", Label: "Pincode", Type: "string", SourcePath: "pincode", Sensitive: true, DefaultVisible: false, Filterable: true},
}

var columnDescriptions = map[string]string{
	"address_count":                   "Number of non-deleted saved addresses currently attached to the customer.",
	"booking_count":                   "Number of bookings matching the selected date range, office, and order-status filter.",
	"beautician_pan":                  "Beautician PAN is sensitive and is shown only to users with report download permission.",
	"bank_transfer":                   "Total positive payments recorded as bank transfer for the order. Refunds, tips, and cancellation fee labels are not counted here.",
	"cash":                            "Total positive cash or COD payments recorded for the order. Refunds, tips, and cancellation fee labels are not counted here.",
	"cancellation_charges":            "Cancellation amount charged to the customer. For cancelled orders, this is the receivable amount counted by the report.",
	"cod_collected_amount":            "COD amount collected from the customer but still pending settlement.",
	"commission_payable":              "Trip commission payable before leaderboard bonus is added.",
	"convenience_fees":                "Convenience fee charged to the customer for this order.",
	"coupon_discount":                 "Discount from coupons or offers after removing membership and special discounts from the total discount.",
	"emp_code":                        "Employee code from the staff profile.",
	"estimated_target1_commission":    "Projected total commission if Target 1 is achieved, calculated by the reporting service.",
	"estimated_target2_commission":    "Projected total commission if Target 2 is achieved, calculated by the reporting service.",
	"hygiene_fees":                    "Hygiene fee charged to the customer for this order.",
	"last_booking_date":               "Latest matching booking date for the customer inside the selected duration.",
	"leaderboard_bonus":               "Bonus added for the staff member's leaderboard position for the selected period.",
	"leaderboard_rank":                "Rank assigned to the staff member for the selected report period.",
	"leave_count":                     "Approved leave entries counted for the staff member in the selected period.",
	"membership_charges":              "Membership amount charged on this order, if any.",
	"membership_discount":             "Discount given because of membership benefits.",
	"net_receivable_bank":             "Online and bank transfer collections after removing payment gateway charges other than tip charges.",
	"net_receivable_including_gst":    "Customer amount due for services and fees, including GST, after discounts. Tips are not included. For cancelled orders, only the cancellation charge is counted.",
	"net_tips_payable":                "Tips collected from the customer after removing payment gateway charges on tips.",
	"online":                          "Total positive UPI, card, wallet, online, or other digital payments recorded for the order. Refunds, tips, and cancellation fee labels are not counted here.",
	"order_count":                     "Number of completed orders counted for the staff member in the selected period.",
	"ot_count":                        "Overtime entries counted for the staff member in the selected period.",
	"payable_amount":                  "Petrol amount payable for included completed trips. Uses calculated fare when available; otherwise calculates from payable distance and office petrol settings.",
	"payable_general_commission":      "General commission payable after applying the report's commission rules.",
	"potential_general_commission":    "General commission that would be payable if the target gate were achieved.",
	"payment_gateway_charges_others":  "Payment gateway charges applied to non-tip payments.",
	"payment_gateway_charges_tips":    "Payment gateway charges applied to tip payments.",
	"petrol_payable":                  "Petrol reimbursement payable for included completed trips. Uses calculated fare when available; otherwise calculates from payable distance and office petrol settings.",
	"refunded":                        "Total refunded amount recorded for the selected period.",
	"special_commission":              "Special commission payable based on configured staff rules.",
	"special_discount":                "One-time or manual discount applied to the order.",
	"surge_charges":                   "Surge amount charged to the customer for this order.",
	"target1_achieved":                "Shows whether the staff member met Target 1 for the selected period.",
	"target2_achieved":                "Shows whether the staff member met Target 2 for the selected period.",
	"target2_bonus":                   "Bonus payable when Target 2 is achieved.",
	"potential_target2_bonus":         "Bonus that would be payable if Target 2 were achieved.",
	"tips":                            "Tip amount paid by the customer for the order.",
	"total_commission":                "Total commission payable after adding eligible commission and bonus amounts.",
	"total_discount_provided":         "Total discount given to the customer: membership discount plus special discount plus coupon discount.",
	"total_distance_km":               "Total payable trip distance in kilometres for included completed trips. Uses fare distance when available, otherwise auto distance, two-way multiplier, and extra kilometres.",
	"total_received":                  "Total customer payment received for the order. If payment history exists, only positive non-tip, non-cancellation-fee entries are counted; otherwise legacy paid amount fields are used.",
	"total_receivable_including_tips": "Net receivable including GST plus customer tips.",
	"total_rider_payable":             "Full rider payable for the period: petrol reimbursement plus trip commission plus leaderboard bonus.",
	"total_revenue":                   "Completed order revenue counted for the staff member in the selected period.",
	"total_services_cost":             "Service subtotal before fees, discounts, tips, and cancellation charges.",
	"total_value":                     "Gross order value before discounts: service subtotal plus convenience, hygiene, surge, membership, and eligible cancellation charges.",
	"trip_count":                      "Number of completed trips counted for the selected period.",
	"upgrade_addon_commission":        "Commission payable for eligible upgrades or add-ons.",
	"zones":                           "Unique zone names from all of the customer's current non-deleted saved addresses.",
}

func withColumnDescriptions(columns []reports.Column) []reports.Column {
	described := make([]reports.Column, len(columns))
	copy(described, columns)
	for index := range described {
		if description, ok := columnDescriptions[described[index].Key]; ok {
			described[index].Description = description
		}
	}
	return described
}
