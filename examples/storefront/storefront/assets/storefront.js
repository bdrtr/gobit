// storefront.js is the shop's whole client.
//
// It is written without a framework and without a build step for the reason the
// panel's two scripts are: the dependency would cost more than the code it saves,
// and an example that needs a toolchain stops being an example of gobit.
//
// It never sees a secret. The publishable key it sends is visible in the page by
// design — its only authority is naming the sales channel — and the cart id it
// keeps is a capability: knowing it IS the right to the cart, which is why it is
// stored and never shown.
(function () {
  "use strict";

  var shop = document.getElementById("shop");
  if (!shop) {
    return;
  }

  var key = shop.getAttribute("data-key");
  var channel = shop.getAttribute("data-channel");
  var listPath = shop.getAttribute("data-list-path");
  var cartPath = shop.getAttribute("data-cart-path");
  var checkoutPath = shop.getAttribute("data-checkout-path");

  // routes are every call this script makes to the store surface, each written
  // as the verb and the template gobit binds it under. No other line in this
  // file names a store address, so the repository can hold every one of them to
  // the routes the tree binds, verb included — internal/arch's shop route gate
  // reads these strings (ADR 0174).
  var routes = {
    regions: "GET /store/v1/regions",
    products: "GET /store/v1/sales-channels/{sales_channel_id}/products",
    product: "GET /store/v1/sales-channels/{sales_channel_id}/products/{id}",
    openCart: "POST /store/v1/carts",
    cart: "GET /store/v1/carts/{id}",
    updateCart: "POST /store/v1/carts/{id}",
    addLine: "POST /store/v1/carts/{id}/line-items",
    shippingAddress: "PUT /store/v1/carts/{id}/shipping-address",
    addShipping: "POST /store/v1/carts/{id}/shipping-methods",
    removeShipping: "DELETE /store/v1/carts/{id}/shipping-methods/{shipping_method_id}",
    complete: "POST /store/v1/carts/{id}/complete",
    shippingOptions: "GET /store/v1/shipping-options",
    paymentProviders: "GET /store/v1/payment-providers",
  };

  // fill puts the values into a route's template, each one escaped for a path.
  // A placeholder with no value is a mistake in this file, and it throws rather
  // than sending the word "undefined" to the shop.
  function fill(template, values) {
    return template.replace(/\{([a-z_]+)\}/g, function (_, name) {
      if (values[name] === undefined || values[name] === null) {
        throw new Error("the route " + template + " needs a value for " + name);
      }

      return encodeURIComponent(values[name]);
    });
  }

  // cartKey is where the cart id is kept between page loads.
  //
  // localStorage rather than a cookie: the id is a capability the SERVER never
  // asks for by itself, so sending it on every request would widen where it can
  // leak. The cost is written down in the example's README — a shared browser
  // keeps the cart.
  var cartKey = "gobit-storefront-cart";

  // call is the one place a request is made, so the verb, the header, the error
  // shape and the empty-answer case are decided once. The verb comes from the
  // route, never from the caller: a call site cannot send a POST to an address
  // written down as a GET.
  //
  // options.values fill the template, options.query becomes the query string,
  // and options.body is sent as JSON.
  function call(route, options) {
    var opts = options || {};
    var space = route.indexOf(" ");
    var path = fill(route.slice(space + 1), opts.values || {});
    if (opts.query) {
      path += "?" + new URLSearchParams(opts.query).toString();
    }

    var request = { method: route.slice(0, space), headers: { "x-publishable-api-key": key } };
    if (opts.body !== undefined) {
      request.headers["content-type"] = "application/json";
      request.body = JSON.stringify(opts.body);
    }

    return fetch(path, request).then(function (response) {
      return response.text().then(function (text) {
        var parsed = text ? JSON.parse(text) : {};
        if (!response.ok) {
          var message = parsed.error && parsed.error.message
            ? parsed.error.message
            : "the shop answered " + response.status;
          throw new Error(message);
        }

        return parsed;
      });
    });
  }

  // show replaces the page's contents. Every value reaches the DOM through a
  // text node; there is no innerHTML path in this file, because a product title
  // is somebody else's text.
  function show(nodes) {
    shop.textContent = "";
    nodes.forEach(function (node) {
      shop.appendChild(node);
    });
  }

  function element(tag, text) {
    var node = document.createElement(tag);
    if (text !== undefined && text !== null) {
      node.appendChild(document.createTextNode(String(text)));
    }

    return node;
  }

  function failure(err) {
    var note = element("p", err.message);
    note.className = "notice";
    show([note]);
  }

  // money formats a minor-unit amount with its currency.
  //
  // The scale is NOT assumed to be a hundred: gobit carries the currency's digit
  // count for exactly this reason, and a page that divides by 100 shows yen a
  // hundred times too small. What this example does instead is print the amount
  // and the code without dividing at all — honest, and one decision less for an
  // example to get wrong.
  function money(price) {
    return price.amount + " " + price.currency_code + " (minor units)";
  }

  function firstPrice(variant) {
    var prices = (variant.price_set && variant.price_set.prices) || [];

    return prices.length ? prices[0] : null;
  }

  // --- the catalog -------------------------------------------------------

  function drawList() {
    call(routes.products, {
      values: { sales_channel_id: channel },
      query: { limit: 20 },
    }).then(function (answer) {
      var products = answer.data || [];
      if (!products.length) {
        var empty = element("p",
          "This channel's catalog is empty. A product is only visible here when it is " +
          "published and the channel carries it — see docs/first-run.md.");
        empty.className = "notice";
        show([empty]);

        return;
      }

      var list = element("ul");
      products.forEach(function (product) {
        var item = element("li");
        var link = element("a", product.title);
        link.setAttribute("href", listPath + "/products/" + encodeURIComponent(product.handle));
        item.appendChild(link);

        var variant = (product.variants || [])[0];
        var price = variant ? firstPrice(variant) : null;
        if (price) {
          var tag = element("span", " — " + money(price));
          tag.className = "muted";
          item.appendChild(tag);
        }
        list.appendChild(item);
      });

      show([list]);
    }).catch(failure);
  }

  // --- one product -------------------------------------------------------

  function drawProduct() {
    var handle = shop.getAttribute("data-handle");

    call(routes.product, { values: { sales_channel_id: channel, id: handle } }).then(function (answer) {
      var product = answer.data;
      var nodes = [element("h2", product.title)];

      if (product.description) {
        nodes.push(element("p", product.description));
      }

      var variants = product.variants || [];
      if (!variants.length) {
        nodes.push(element("p", "This product has no variant to sell."));
        show(nodes);

        return;
      }

      variants.forEach(function (variant) {
        var row = element("p");
        var price = firstPrice(variant);
        row.appendChild(document.createTextNode(
          variant.title + (price ? " — " + money(price) : " — no price in this currency")
        ));

        if (price) {
          var button = element("button", "Add to cart");
          button.addEventListener("click", function () {
            addToCart(variant.id, button);
          });
          row.appendChild(document.createTextNode(" "));
          row.appendChild(button);
        }
        nodes.push(row);
      });

      show(nodes);
    }).catch(failure);
  }

  // --- the cart ----------------------------------------------------------

  // openCart returns the stored cart, or opens one.
  //
  // The body carries the COUNTRY and nothing else: the server derives the region
  // and the currency from it, and sending either is refused with 422. The country
  // comes from the shop's own regions, so the example does not hard-code one.
  function openCart() {
    var existing = null;
    try {
      existing = window.localStorage.getItem(cartKey);
    } catch (err) {
      existing = null;
    }
    if (existing) {
      return Promise.resolve(existing);
    }

    return call(routes.regions).then(function (answer) {
      var regions = answer.data || [];
      var country = null;
      regions.some(function (region) {
        var countries = region.countries || [];
        if (countries.length) {
          country = countries[0].code;

          return true;
        }

        return false;
      });

      if (!country) {
        throw new Error(
          "no region serves a country yet, so no cart can be opened — " +
          "docs/first-run.md step 3 binds one"
        );
      }

      return call(routes.openCart, { body: { country_code: country } }).then(function (created) {
        var id = created.data.id;
        try {
          window.localStorage.setItem(cartKey, id);
        } catch (err) {
          // A browser that refuses storage gets a cart per page load. The shop
          // still works; it just forgets.
        }

        return id;
      });
    });
  }

  function addToCart(variantID, button) {
    button.disabled = true;
    openCart().then(function (cartID) {
      return call(routes.addLine, {
        values: { id: cartID },
        body: { variant_id: variantID, quantity: 1 },
      });
    }).then(function () {
      window.location.href = cartPath;
    }).catch(function (err) {
      button.disabled = false;
      failure(err);
    });
  }

  // storedCart is the kept cart id, or null.
  function storedCart() {
    try {
      return window.localStorage.getItem(cartKey);
    } catch (err) {
      return null;
    }
  }

  function forgetCart() {
    try {
      window.localStorage.removeItem(cartKey);
    } catch (err) {
      // Nothing to forget in a browser that refused storage.
    }
  }

  function drawCart() {
    var id = storedCart();
    if (!id) {
      show([element("p", "The cart is empty.")]);

      return;
    }

    call(routes.cart, { values: { id: id } }).then(function (answer) {
      var cart = answer.data;
      var items = cart.items || [];
      var nodes = [element("h2", "Cart")];

      if (!items.length) {
        nodes.push(element("p", "The cart is empty."));
        show(nodes);

        return;
      }

      var list = element("ul");
      items.forEach(function (item) {
        list.appendChild(element("li",
          item.title + " x" + item.quantity + " — " + item.total + " " + cart.currency_code));
      });
      nodes.push(list);
      nodes.push(element("p",
        "Total " + cart.total + " " + cart.currency_code + " (minor units), tax " + cart.tax_total));
      var onward = element("a", "Checkout");
      onward.setAttribute("href", checkoutPath);
      var row = element("p");
      row.appendChild(onward);
      nodes.push(row);

      show(nodes);
    }).catch(failure);
  }

  // --- checkout ------------------------------------------------------------

  // The checkout is four steps, and the shop's own server decides nothing in any
  // of them: the address, a shipping option, a payment provider, and the
  // completion. The total the shopper approves is sent back as expected_total, so
  // a price that changed under the page refuses the order instead of charging a
  // figure nobody saw.

  // field is one labelled input of the address form.
  function field(form, name, label, required) {
    var row = element("p");
    var tag = element("label", label + " ");
    var input = document.createElement("input");
    input.name = name;
    input.required = !!required;
    tag.appendChild(input);
    row.appendChild(tag);
    form.appendChild(row);

    return input;
  }

  function drawCheckout() {
    var id = storedCart();
    if (!id) {
      show([element("p", "The cart is empty, so there is nothing to check out.")]);

      return;
    }

    Promise.all([call(routes.cart, { values: { id: id } }), call(routes.regions)]).then(function (answers) {
      var cart = answers[0].data;
      if (!(cart.items || []).length) {
        show([element("p", "The cart is empty, so there is nothing to check out.")]);

        return;
      }

      // The address has to be in a country of the cart's region: the region was
      // derived from a country when the cart was opened, and it prices, taxes and
      // ships for its own countries only. The cart does not say which of them it
      // was opened with, so the shopper chooses among the region's.
      var countries = [];
      (answers[1].data || []).forEach(function (region) {
        if (region.id === cart.region_id) {
          (region.countries || []).forEach(function (country) {
            countries.push(country.code);
          });
        }
      });
      if (!countries.length) {
        throw new Error("the cart's region serves no country, so no address can be taken");
      }

      var form = element("form");
      var inputs = {
        email: field(form, "email", "E-mail", true),
        first_name: field(form, "first_name", "First name", true),
        last_name: field(form, "last_name", "Last name", true),
        address_1: field(form, "address_1", "Address", true),
        city: field(form, "city", "City", true),
        postal_code: field(form, "postal_code", "Postal code", true),
      };
      var countryRow = element("p");
      var countryTag = element("label", "Country ");
      var country = document.createElement("select");
      country.name = "country_code";
      countries.forEach(function (code) {
        var option = element("option", code);
        option.value = code;
        country.appendChild(option);
      });
      countryTag.appendChild(country);
      countryRow.appendChild(countryTag);
      form.appendChild(countryRow);
      var submit = element("button", "Continue to shipping");
      submit.type = "submit";
      form.appendChild(submit);

      // The page never navigates: the shop's policy says form-action 'none',
      // and the form is only a way to collect the fields.
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        submit.disabled = true;
        saveAddress(cart.id, inputs, country.value).then(function () {
          return chooseShipping(id);
        }).catch(function (err) {
          submit.disabled = false;
          failure(err);
        });
      });

      show([element("h2", "Checkout"), form]);
    }).catch(failure);
  }

  // saveAddress writes the e-mail and the shipping address.
  function saveAddress(id, inputs, countryCode) {
    var address = { country_code: countryCode };
    Object.keys(inputs).forEach(function (name) {
      if (name !== "email") {
        address[name] = inputs[name].value;
      }
    });

    return call(routes.updateCart, {
      values: { id: id },
      body: { email: inputs.email.value },
    }).then(function () {
      return call(routes.shippingAddress, { values: { id: id }, body: address });
    });
  }

  // chooseShipping lists the options the cart can use and puts the chosen one on
  // it.
  //
  // The options are asked for with the cart's own region, currency and country,
  // and the price shown is the one the server quotes when the option is added.
  function chooseShipping(id) {
    return call(routes.cart, { values: { id: id } }).then(function (answer) {
      var cart = answer.data;
      var held = cart.shipping_methods || [];

      return call(routes.shippingOptions, {
        query: {
          region_id: cart.region_id,
          currency_code: cart.currency_code,
          country_code: (cart.shipping_address && cart.shipping_address.country_code) || "",
        },
      }).then(function (listed) {
        var options = listed.data || [];
        if (!options.length) {
          throw new Error("no shipping option serves this address yet — " +
            "docs/first-run.md step 9 creates one");
        }

        var list = element("ul");
        options.forEach(function (option) {
          var item = element("li", option.name + " — " + option.amount + " " +
            option.currency_code + " (minor units) ");
          var pick = element("button", "Ship this way");
          pick.addEventListener("click", function () {
            pick.disabled = true;
            replaceShipping(id, held, option.id).then(function () {
              return choosePayment(id);
            }).catch(failure);
          });
          item.appendChild(pick);
          list.appendChild(item);
        });

        show([element("h2", "Shipping"), list]);
      });
    });
  }

  // replaceShipping makes the chosen option the cart's only delivery.
  //
  // A shopper who comes back to the checkout — after a refused payment, or to
  // change their mind — finds the cart still holding the method they chose
  // before. Adding the same option again is refused, and adding a different one
  // would charge two deliveries, so the methods the cart holds for other options
  // are removed and the chosen one is added only if it is not already there. A
  // shop that splits a cart across shipping profiles would keep one method per
  // profile instead; this one has a single choice.
  //
  // The writes go one after another rather than together: each one reprices the
  // cart, and two repricings racing on one cart would retry against each other.
  function replaceShipping(id, held, optionID) {
    var kept = false;
    var done = Promise.resolve();
    held.forEach(function (method) {
      if (method.shipping_option_id === optionID) {
        kept = true;

        return;
      }
      done = done.then(function () {
        return call(routes.removeShipping, { values: { id: id, shipping_method_id: method.id } });
      });
    });

    return done.then(function () {
      if (kept) {
        return null;
      }

      return call(routes.addShipping, {
        values: { id: id },
        body: { shipping_option_id: optionID },
      });
    });
  }

  // choosePayment lists the providers and shows the total to approve.
  //
  // The total is the cart's own, read after the delivery was added: every write
  // the checkout makes leaves the cart repriced (ADR 0173). A cart that still
  // says its totals are stale has no figure to approve, and the page says so
  // rather than asking the shopper to agree to one.
  function choosePayment(id) {
    return Promise.all([
      call(routes.cart, { values: { id: id } }),
      call(routes.paymentProviders),
    ]).then(function (answers) {
      var cart = answers[0].data;
      if (cart.totals_stale) {
        throw new Error("the cart's total is not up to date, so there is nothing to approve yet — " +
          "reload the page");
      }
      var providers = answers[1].data || [];
      if (!providers.length) {
        throw new Error("no payment provider is installed");
      }

      var nodes = [
        element("h2", "Payment"),
        element("p", "Total " + cart.total + " " + cart.currency_code +
          " (minor units), shipping " + cart.shipping_total + ", tax " + cart.tax_total),
      ];
      var list = element("ul");
      providers.forEach(function (provider) {
        var item = element("li", provider + " ");
        var pay = element("button", "Place the order");
        pay.addEventListener("click", function () {
          pay.disabled = true;
          placeOrder(id, provider, cart.total).catch(function (err) {
            pay.disabled = false;
            failure(err);
          });
        });
        item.appendChild(pay);
        list.appendChild(item);
      });
      nodes.push(list);

      show(nodes);
    });
  }

  // placeOrder completes the cart with the total the shopper was shown.
  function placeOrder(id, provider, total) {
    return call(routes.complete, {
      values: { id: id },
      body: { payment_provider_id: provider, expected_total: total },
    }).then(function (answer) {
      forgetCart();
      show([
        element("h2", "Thank you"),
        element("p", "Order " + answer.data.order_id + " was placed for " +
          answer.data.total + " " + answer.data.currency_code + " (minor units)."),
      ]);
    });
  }

  var mounts = { list: drawList, product: drawProduct, cart: drawCart, checkout: drawCheckout };
  var draw = mounts[shop.getAttribute("data-mount")];
  if (draw) {
    draw();
  }
})();
