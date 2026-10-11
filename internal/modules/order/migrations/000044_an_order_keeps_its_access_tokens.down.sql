-- Rolling back drops every token an order handed out, and every link that
-- carried one stops opening its order.
DROP TABLE IF EXISTS order_access_tokens;
