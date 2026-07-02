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
	{Key: "upgrade_addon_commission", Label: "Upgrade/Add-on Commission", Type: "currency", SourcePath: "upgrade_addon_commission", FormulaID: "commission.upgrade_addon", ContributesToTotal: true, DefaultVisible: true},
	{Key: "refunded", Label: "Refunded", Type: "currency", SourcePath: "refunded", FormulaID: "payment.refunded", ContributesToTotal: true, DefaultVisible: true},
	{Key: "target2_bonus", Label: "Target 2 Bonus", Type: "currency", SourcePath: "target2_bonus", FormulaID: "beautician.target2_bonus", ContributesToTotal: true, DefaultVisible: true},
	{Key: "leaderboard_rank", Label: "Leaderboard Rank", Type: "string", SourcePath: "leaderboard_rank", FormulaID: "beautician.leaderboard_rank", DefaultVisible: true},
	{Key: "leaderboard_bonus", Label: "Leaderboard Bonus", Type: "currency", SourcePath: "leaderboard_bonus", FormulaID: "beautician.leaderboard_bonus", ContributesToTotal: true, DefaultVisible: true},
	{Key: "total_commission", Label: "Total Commission", Type: "currency", SourcePath: "total_commission", FormulaID: "beautician.total_commission", ContributesToTotal: true, DefaultVisible: true},
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
